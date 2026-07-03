#!/usr/bin/env python3
"""
ETL: Django (SOURCE) -> Go backend Postgres (TARGET).

SOURCE: postgresql://postgres:postgres@localhost:5435/django   (container django-src)
TARGET: postgresql://postgres:23092005@localhost:5433/botspace (container botspace-pg)

Every Django integer PK is mapped deterministically to a UUID via uuid5(NS, "table:pk"),
so the script is idempotent / re-runnable and FK references stay consistent without
needing an in-memory id map.

Run:  python3 scripts/etl_django.py
"""
import decimal
import json
import sys
import traceback
import uuid

import bcrypt
import psycopg2
import psycopg2.extras

import os

SRC_DSN = os.environ.get("ETL_SRC_DSN", "postgresql://postgres:postgres@localhost:5435/django")
DST_DSN = os.environ.get("ETL_DST_DSN", "postgresql://postgres:23092005@localhost:5433/botspace")

# SAFETY: the TARGET is TRUNCATEd+reimported. Refuse to run unless it is the
# new Go backend DB (botspace_go) — never let a mis-set DSN wipe Django's DB.
if "botspace_go" not in DST_DSN:
    raise SystemExit(f"REFUSING: ETL target must be botspace_go, got: {DST_DSN!r}")

NS = uuid.UUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

REPORT = []  # (name, src_count, dst_inserted, note)
SKIPPED_NOTES = []


def uid(table, pk):
    if pk is None:
        return None
    return str(uuid.uuid5(NS, f"{table}:{pk}"))


def txt(v, default=""):
    if v is None:
        return default
    return v


def nz_txt(v):
    """Empty string -> None (for nullable+unique columns like phone)."""
    if v is None or v == "":
        return None
    return v


def dec(v, default="0"):
    if v is None:
        return decimal.Decimal(default)
    if isinstance(v, decimal.Decimal):
        return v
    try:
        return decimal.Decimal(str(v))
    except Exception:
        return decimal.Decimal(default)


def js(v, default):
    if v is None:
        v = default
    return psycopg2.extras.Json(v)


def field_default_value(v):
    if v is None or v == "":
        return None
    if v in ("True", "False"):
        return psycopg2.extras.Json(v == "True")
    try:
        return psycopg2.extras.Json(json.loads(v))
    except Exception:
        return psycopg2.extras.Json(v)


FIELD_TYPE_MAP = {
    "photo": "image",
    "number": "float",
    "file": "file",
    "video": "video",
    "relation": "relation",
    "bool": "bool",
    "datetime": "datetime",
    "text": "text",
}


def map_field_type(t):
    return FIELD_TYPE_MAP.get(t, "text")


RELASE_STATUS_MAP = {"init": "initializing"}


def map_relase_status(s):
    if not s:
        return "pending"
    return RELASE_STATUS_MAP.get(s, s)


VALID_DEBUG_STATUS = {"pending", "initializing", "running", "stopped", "stopping", "failed", "suspended"}


def map_debug_status(s):
    if s in VALID_DEBUG_STATUS:
        return s
    return "stopped"


TRUNCATE_TABLES = [
    "plan", "users", "projects", "relase", "debug", "enviroment", "file", "resource",
    "project_resource_snapshot", "debug_run_log", "message", "module", "project_module",
    "webhook_endpoint", "managed_bot", "credential", "transaction", "payment_order",
    "payment_withdrawal_request", "history", "template_category", "template_feature",
    "template", "template_category_map", "template_feature_map", "template_like",
    "usertemplate", "template_order", "template_submission_request",
    "template_submission_category_map", "template_submission_feature_map",
    "template_version", "template_withdrawal_request", "collection", "field",
    "cms_export_task", "authbot_bot_users", "authbot_codes", "user_verification",
    "audit_log", "user_subscription",
]


def run(dst_conn, src_cur, name, src_sql, dst_table, dst_columns, transform, conflict_cols="id"):
    """Fetch all rows from src_sql, transform each into a tuple matching dst_columns,
    bulk insert with ON CONFLICT DO NOTHING. Any failure is caught, logged, and does
    not abort the rest of the migration."""
    try:
        src_cur.execute(src_sql)
        rows = src_cur.fetchall()
        values = []
        row_errors = 0
        for row in rows:
            try:
                t = transform(row)
                if t is not None:
                    values.append(t)
            except Exception as e:
                row_errors += 1
                if row_errors <= 3:
                    print(f"    [row-skip] {name} id={row.get('id')}: {e}")
        dst_cur = dst_conn.cursor()
        if values:
            cols_sql = ", ".join(dst_columns)
            insert_sql = (
                f"INSERT INTO {dst_table} ({cols_sql}) VALUES %s "
                f"ON CONFLICT ({conflict_cols}) DO NOTHING"
            )
            psycopg2.extras.execute_values(dst_cur, insert_sql, values, page_size=1000)
        dst_conn.commit()
        note = f"{row_errors} row errors" if row_errors else ""
        print(f"[OK]   {name}: src={len(rows)} attempted={len(values)} {note}")
        REPORT.append((name, len(rows), len(values), note))
        return len(rows)
    except Exception as e:
        dst_conn.rollback()
        print(f"[FAIL] {name}: {e}")
        traceback.print_exc()
        REPORT.append((name, "?", 0, f"FAILED: {e}"))
        return None


def main():
    src_conn = psycopg2.connect(SRC_DSN)
    src_conn.set_session(readonly=True, autocommit=True)
    src_cur = src_conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)

    dst_conn = psycopg2.connect(DST_DSN)

    # ---- 0. Truncate target business tables (idempotent re-run) ----
    print("== Truncating target business tables ==")
    with dst_conn.cursor() as c:
        c.execute("TRUNCATE TABLE " + ", ".join(TRUNCATE_TABLES) + " CASCADE")
    dst_conn.commit()

    # ---- 1. plan ----
    run(
        dst_conn, src_cur, "plan -> plan",
        "SELECT * FROM plan",
        "plan",
        ["id", "name", "price", "description", "is_top", "is_active", "sort_order",
         "max_bots", "max_tokens", "max_output_tokens", "max_input_tokens",
         "cached_token_rate", "features", "created_at", "updated_at"],
        lambda r: (
            uid("plan", r["id"]), txt(r["name"]), dec(r["price"]), r["description"],
            bool(r["is_top"]), bool(r["is_active"]), r["order"] or 0,
            r["max_bots"] or 1, r["max_tokens"] or 1000000,
            r["max_output_tokens"] or 200000, r["max_input_tokens"] or 800000,
            dec(r["cached_token_rate"], "0.10"), js(r["features"], {}),
            r["created_at"], r["updated_at"],
        ),
        conflict_cols="name",
    )

    # ---- 2. accounts_user -> users ----
    run(
        dst_conn, src_cur, "accounts_user -> users",
        "SELECT * FROM accounts_user",
        "users",
        ["id", "phone", "first_name", "last_name", "username", "email", "password_hash",
         "role", "balance", "template_balance", "is_active", "is_staff", "is_superuser",
         "validated_at", "kyc_verified_at", "created_at", "updated_at"],
        lambda r: (
            uid("accounts_user", r["id"]), nz_txt(r["phone"]), txt(r["first_name"]),
            txt(r["last_name"]), r["username"] or None, r["email"] or None,
            txt(r["password"]),
            # Django gates admin on is_superuser (bool); the Go backend gates on
            # the `role` string (RequireRole "admin"/"superuser"). Map so Django
            # superusers keep admin access after import.
            ("superuser" if r["is_superuser"] else txt(r["role"], "user")),
            r["balance"] or 0,
            dec(r["template_balance"]), bool(r["is_active"]), bool(r["is_staff"]),
            bool(r["is_superuser"]), r["validated_at"], r["kyc_verified_at"],
            r["created_at"] or r["date_joined"], r["updated_at"] or r["date_joined"],
        ),
    )

    # ---- 3. user_subscription ----
    run(
        dst_conn, src_cur, "user_subscription -> user_subscription",
        "SELECT * FROM user_subscription",
        "user_subscription",
        ["id", "user_id", "plan_id", "status", "started_at", "expires_at",
         "tokens_used_input", "tokens_used_output", "token_reset_at", "bots_count",
         "created_at", "updated_at"],
        lambda r: (
            uid("user_subscription", r["id"]), uid("accounts_user", r["user_id"]),
            uid("plan", r["plan_id"]), txt(r["status"], "active"),
            r["created_at"], r["expires_at"], r["tokens_used_input"] or 0,
            r["tokens_used_output"] or 0, r["token_reset_at"] or r["created_at"],
            r["bots_count"] or 0, r["created_at"], r["updated_at"],
        ),
    )

    # ---- 4. project -> projects ----
    run(
        dst_conn, src_cur, "project -> projects",
        "SELECT * FROM project",
        "projects",
        ["id", "uuid", "name", "description", "username", "avatar_file_id", "owner_id",
         "flow", "is_template", "release_webhook_uuid", "debug_webhook_uuid",
         "mongo_username", "mongo_password", "mongo_database", "created_at",
         "updated_at", "deleted_at"],
        lambda r: (
            uid("project", r["id"]), r["uuid"], txt(r["name"]), r["description"],
            r["username"], r["avatar_file_id"], uid("accounts_user", r["owner_id"]),
            js(r["flow"], []), bool(r["is_template"]), r["release_webhook_uuid"],
            r["debug_webhook_uuid"], r["mongo_username"], r["mongo_password"],
            r["mongo_database"], r["created_at"], r["updated_at"], r["deleted_at"],
        ),
    )

    # ---- 5. relase, debug, enviroment ----
    run(
        dst_conn, src_cur, "relase -> relase",
        "SELECT * FROM relase",
        "relase",
        ["id", "project_id", "version", "flow", "service_id", "status", "created_at", "updated_at"],
        lambda r: (
            uid("relase", r["id"]), uid("project", r["project_id"]), r["version"] or 1,
            js(r["flow"], []), r["service_id"], map_relase_status(r["status"]),
            r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "debug -> debug",
        "SELECT * FROM debug",
        "debug",
        ["id", "project_id", "flow_hash", "service_id", "status", "created_at", "updated_at"],
        lambda r: (
            uid("debug", r["id"]), uid("project", r["project_id"]), r["flow_hash"],
            r["service_id"], map_debug_status(r["status"]), r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "enviroment -> enviroment",
        "SELECT * FROM enviroment",
        "enviroment",
        ["id", "project_id", "user_id", "key", "value", "secret", "created_at", "updated_at"],
        lambda r: (
            uid("enviroment", r["id"]), uid("project", r["project_id"]),
            uid("accounts_user", r["user_id"]), txt(r["key"]), txt(r["value"]),
            bool(r["secret"]), r["created_at"], r["updated_at"],
        ),
    )

    # ---- 6. file, resource, project_resource_snapshot, debug_run_log ----
    run(
        dst_conn, src_cur, "file -> file",
        "SELECT * FROM file",
        "file",
        ["id", "uuid", "project_id", "user_id", "file_path", "original_name", "filesize",
         "filetype", "expires_at", "created_at", "updated_at"],
        lambda r: (
            uid("file", r["id"]), r["uuid"], uid("project", r["project_id"]),
            uid("accounts_user", r["user_id"]), txt(r["file"]), "", r["filesize"] or 0,
            txt(r["filetype"], "file"), r["expires_at"], r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "resource -> resource",
        "SELECT * FROM resource",
        "resource",
        ["id", "project_id", "user_id", "hostname", "ram", "cpu", "net_rx", "net_tx",
         "created_at", "updated_at"],
        lambda r: (
            uid("resource", r["id"]), uid("project", r["project_id"]),
            uid("accounts_user", r["user_id"]), txt(r["hostname"]), r["ram"] or 0,
            r["cpu"] or 0, r["net_rx"] or 0, r["net_tx"] or 0, r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "api_projectresourcesnapshot -> project_resource_snapshot",
        "SELECT * FROM api_projectresourcesnapshot",
        "project_resource_snapshot",
        ["id", "project_id", "disk_bytes", "disk_files_bytes", "disk_cms_bytes", "created_at"],
        lambda r: (
            uid("api_projectresourcesnapshot", r["id"]), uid("project", r["project_id"]),
            r["disk_bytes"] or 0, r["disk_files_bytes"] or 0, r["disk_cms_bytes"] or 0,
            r["created_at"],
        ),
    )
    run(
        dst_conn, src_cur, "api_debugrunlog -> debug_run_log",
        "SELECT * FROM api_debugrunlog",
        "debug_run_log",
        ["id", "project_id", "user_id", "created_at"],
        lambda r: (
            uid("api_debugrunlog", r["id"]), uid("project", r["project_id"]),
            uid("accounts_user", r["user_id"]) if r["user_id"] else None, r["created_at"],
        ),
    )
    SKIPPED_NOTES.append("storageresource (1.6M rows) SKIPPED per instructions — low value, not migrated.")

    # ---- 7. message ----
    run(
        dst_conn, src_cur, "message -> message",
        "SELECT * FROM message",
        "message",
        ["id", "project_id", "caption", "message_type", "file_id", "button_type",
         "buttons", "total", "done", "fail", "status"],
        lambda r: (
            uid("message", r["id"]), uid("project", r["project_id"]), txt(r["caption"]),
            txt(r["message_type"], "text"), r["file_id"] or None,
            txt(r["button_type"], "reply"), js(r["buttons"], []), r["total"] or 0,
            r["done"] or 0, r["fail"] or 0, txt(r["status"], "progress"),
        ),
    )
    SKIPPED_NOTES.append("message.file (local Django media path) has no target column — only file_id (telegram cache) migrated.")

    # ---- 8. module, project_module ----
    run(
        dst_conn, src_cur, "module -> module",
        "SELECT * FROM module",
        "module",
        ["id", "slug", "name", "description", "icon", "github_url", "image", "port",
         "auth_token", "manifest_cache", "manifest_fetched_at", "active", "is_default",
         "created_at", "updated_at"],
        lambda r: (
            uid("module", r["id"]), txt(r["slug"]), txt(r["name"]), r["description"],
            r["icon"], r["github_url"], txt(r["image"]), r["port"] or 8100,
            r["auth_token"],
            psycopg2.extras.Json(r["manifest_cache"]) if r["manifest_cache"] is not None else None,
            r["manifest_fetched_at"],
            bool(r["active"]), bool(r["is_default"]), r["created_at"], r["updated_at"],
        ),
        conflict_cols="slug",
    )
    run(
        dst_conn, src_cur, "project_module -> project_module",
        "SELECT * FROM project_module",
        "project_module",
        ["id", "project_id", "module_id", "enabled", "config", "service_id", "endpoint",
         "status", "created_at", "updated_at"],
        lambda r: (
            uid("project_module", r["id"]), uid("project", r["project_id"]),
            uid("module", r["module_id"]), bool(r["enabled"]), js(r["config"], {}),
            r["service_id"], r["endpoint"], txt(r["status"], "pending"),
            r["created_at"], r["updated_at"],
        ),
    )

    # ---- 9. webhook_endpoint, managed_bot ----
    run(
        dst_conn, src_cur, "webhook_endpoint -> webhook_endpoint",
        "SELECT * FROM webhook_endpoint",
        "webhook_endpoint",
        ["id", "token", "project_id", "node_id", "chat_id", "created_at", "updated_at"],
        lambda r: (
            uid("webhook_endpoint", r["id"]), r["token"], uid("project", r["project_id"]),
            txt(r["node_id"]), r["chat_id"] or 0, r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "managed_bot -> managed_bot",
        "SELECT * FROM managed_bot",
        "managed_bot",
        ["id", "owner_id", "tg_bot_id", "username", "name", "token_encrypted", "used",
         "created_at", "updated_at"],
        lambda r: (
            uid("managed_bot", r["id"]),
            uid("accounts_user", r["owner_id"]) if r["owner_id"] else None,
            r["tg_bot_id"], txt(r["username"]), txt(r["name"]), txt(r["token_encrypted"]),
            bool(r["used"]), r["created_at"], r["updated_at"],
        ),
    )

    # ---- 10. credential ----
    run(
        dst_conn, src_cur, "credential -> credential",
        "SELECT * FROM credential",
        "credential",
        ["id", "owner_id", "type_key", "name", "data_encrypted", "created_at",
         "updated_at", "deleted_at"],
        lambda r: (
            uid("credential", r["id"]), uid("accounts_user", r["owner_id"]),
            txt(r["type_key"]), txt(r["name"]), txt(r["data_encrypted"]),
            r["created_at"], r["updated_at"], r["deleted_at"],
        ),
    )
    SKIPPED_NOTES.append(
        "credential.data_encrypted kept AS-IS (Fernet-encrypted with the Django SECRET_KEY). "
        "The Go backend uses AES-256-GCM with a different key/format and CANNOT decrypt these "
        "rows; they exist for record-keeping only until re-entered by the owner."
    )

    # ---- 11. transaction, payment_order, payment_withdrawal_request, history ----
    run(
        dst_conn, src_cur, "transaction -> transaction",
        "SELECT * FROM transaction",
        "transaction",
        ["id", "user_id", "amount", "status", "idempotency_key", "created_at",
         "updated_at", "deleted_at"],
        lambda r: (
            uid("transaction", r["id"]), uid("accounts_user", r["user_id"]),
            dec(r["amount"]), bool(r["status"]), r["idempotency_key"] or None,
            r["created_at"], r["updated_at"], r["deleted_at"],
        ),
    )
    run(
        dst_conn, src_cur, "payment_order -> payment_order",
        "SELECT * FROM payment_order",
        "payment_order",
        ["id", "project_id", "owner_id", "payer_chat_id", "payer_first_name",
         "payer_last_name", "payer_username", "amount", "commission", "net_amount",
         "provider", "status", "external_id", "comment", "idempotency_key",
         "transaction_id", "paid_at", "is_debug", "pay_instruction", "created_at",
         "updated_at", "deleted_at"],
        lambda r: (
            uid("payment_order", r["id"]), uid("project", r["project_id"]),
            uid("accounts_user", r["owner_id"]), r["payer_chat_id"] or 0,
            r["payer_first_name"], r["payer_last_name"], r["payer_username"],
            dec(r["amount"]), dec(r["commission"]), dec(r["net_amount"]),
            txt(r["provider"], "click"), txt(r["status"], "created"), r["external_id"],
            r["comment"], r["idempotency_key"] or None,
            uid("transaction", r["transaction_id"]) if r["transaction_id"] else None,
            r["paid_at"], bool(r["is_debug"]), txt(r["pay_instruction"]),
            r["created_at"], r["updated_at"], r["deleted_at"],
        ),
    )
    run(
        dst_conn, src_cur, "payment_withdrawal_request -> payment_withdrawal_request",
        "SELECT * FROM payment_withdrawal_request",
        "payment_withdrawal_request",
        ["id", "user_id", "project_id", "amount", "card_number", "card_owner", "status",
         "rejection_reason", "reviewed_by", "reviewed_at", "created_at", "updated_at"],
        lambda r: (
            uid("payment_withdrawal_request", r["id"]), uid("accounts_user", r["user_id"]),
            uid("project", r["project_id"]) if r["project_id"] else None,
            dec(r["amount"]), txt(r["card_number"]), txt(r["card_owner"]),
            txt(r["status"], "pending"), r["rejection_reason"],
            uid("accounts_user", r["reviewed_by_id"]) if r["reviewed_by_id"] else None,
            r["reviewed_at"], r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "history -> history",
        "SELECT * FROM history",
        "history",
        ["id", "user_id", "amount", "comment", "type", "idempotency_key", "created_at",
         "updated_at", "deleted_at"],
        lambda r: (
            uid("history", r["id"]), uid("accounts_user", r["user_id"]) if r["user_id"] else None,
            dec(r["amount"]), r["comment"], txt(r["type"], "income"),
            r["idempotency_key"] or None, r["created_at"], r["updated_at"], r["deleted_at"],
        ),
    )

    # ---- 12. template marketplace ----
    run(
        dst_conn, src_cur, "template_category -> template_category",
        "SELECT * FROM template_category",
        "template_category",
        ["id", "name", "created_at", "updated_at"],
        lambda r: (uid("template_category", r["id"]), txt(r["name"]), r["created_at"], r["updated_at"]),
    )
    run(
        dst_conn, src_cur, "template_feature -> template_feature",
        "SELECT * FROM template_feature",
        "template_feature",
        ["id", "name", "value", "created_at", "updated_at"],
        lambda r: (uid("template_feature", r["id"]), txt(r["name"]), r["value"], r["created_at"], r["updated_at"]),
    )
    run(
        dst_conn, src_cur, "template -> template",
        "SELECT * FROM template",
        "template",
        ["id", "author_id", "project_id", "name", "slug", "image", "video", "spec",
         "short_description", "description", "original_price", "discount",
         "discount_type", "views", "version", "is_verify", "demo_url", "required_envs",
         "created_at", "updated_at"],
        lambda r: (
            uid("template", r["id"]),
            uid("accounts_user", r["author_id"]) if r["author_id"] else None,
            uid("project", r["project_id"]) if r["project_id"] else None,
            txt(r["name"]), txt(r["slug"]), txt(r["image"]), r["video"],
            js(r["spec"], {}), txt(r["short_description"]), txt(r["description"]),
            dec(r["original_price"]), dec(r["discount"]), txt(r["discount_type"], "percent"),
            r["views"] or 0, r["version"] or 1, bool(r["is_verify"]), r["demo_url"],
            js(r["required_envs"], []), r["created_at"], r["updated_at"],
        ),
        conflict_cols="slug",
    )
    run(
        dst_conn, src_cur, "template_categories -> template_category_map",
        "SELECT * FROM template_categories",
        "template_category_map",
        ["template_id", "category_id"],
        lambda r: (uid("template", r["templatemodel_id"]), uid("template_category", r["templatecategorymodel_id"])),
        conflict_cols="template_id, category_id",
    )
    run(
        dst_conn, src_cur, "template_features -> template_feature_map",
        "SELECT * FROM template_features",
        "template_feature_map",
        ["template_id", "feature_id"],
        lambda r: (uid("template", r["templatemodel_id"]), uid("template_feature", r["templatefeaturemodel_id"])),
        conflict_cols="template_id, feature_id",
    )
    run(
        dst_conn, src_cur, "template_like -> template_like",
        "SELECT * FROM template_like",
        "template_like",
        ["id", "template_id", "user_id", "created_at"],
        lambda r: (
            uid("template_like", r["id"]), uid("template", r["template_id"]),
            uid("accounts_user", r["user_id"]) if r["user_id"] else None, r["created_at"],
        ),
    )
    run(
        dst_conn, src_cur, "usertemplate -> usertemplate",
        "SELECT * FROM usertemplate",
        "usertemplate",
        ["id", "user_id", "template_id", "created_at", "updated_at"],
        lambda r: (
            uid("usertemplate", r["id"]), uid("accounts_user", r["user_id"]),
            uid("template", r["template_id"]), r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "templae_order -> template_order",
        "SELECT * FROM templae_order",
        "template_order",
        ["id", "user_id", "template_id", "amount", "status", "payment_status",
         "commission_paid", "created_at", "updated_at"],
        lambda r: (
            uid("template_order", r["id"]),
            uid("accounts_user", r["user_id"]) if r["user_id"] else None,
            uid("template", r["template_id"]) if r["template_id"] else None,
            dec(r["amount"]), txt(r["status"], "pending"), bool(r["payment_status"]),
            bool(r["commission_paid"]), r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "template_submission_request -> template_submission_request",
        "SELECT * FROM template_submission_request",
        "template_submission_request",
        ["id", "user_id", "project_id", "name", "short_description", "description",
         "image", "video", "demo_url", "original_price", "status", "rejection_reason",
         "template_id", "reviewed_by", "reviewed_at", "required_envs", "created_at", "updated_at"],
        lambda r: (
            uid("template_submission_request", r["id"]), uid("accounts_user", r["user_id"]),
            uid("project", r["project_id"]), txt(r["name"]), txt(r["short_description"]),
            txt(r["description"]), r["image"], r["video"], r["demo_url"],
            dec(r["original_price"]), txt(r["status"], "pending"), r["rejection_reason"],
            uid("template", r["template_id"]) if r["template_id"] else None,
            uid("accounts_user", r["reviewed_by_id"]) if r["reviewed_by_id"] else None,
            r["reviewed_at"], js(r["required_envs"], []), r["created_at"], r["updated_at"],
        ),
    )
    run(
        dst_conn, src_cur, "template_submission_request_categories -> template_submission_category_map",
        "SELECT * FROM template_submission_request_categories",
        "template_submission_category_map",
        ["submission_id", "category_id"],
        lambda r: (
            uid("template_submission_request", r["templatesubmissionrequestmodel_id"]),
            uid("template_category", r["templatecategorymodel_id"]),
        ),
        conflict_cols="submission_id, category_id",
    )
    run(
        dst_conn, src_cur, "template_submission_request_features -> template_submission_feature_map",
        "SELECT * FROM template_submission_request_features",
        "template_submission_feature_map",
        ["submission_id", "feature_id"],
        lambda r: (
            uid("template_submission_request", r["templatesubmissionrequestmodel_id"]),
            uid("template_feature", r["templatefeaturemodel_id"]),
        ),
        conflict_cols="submission_id, feature_id",
    )
    run(
        dst_conn, src_cur, "template_version -> template_version",
        "SELECT * FROM template_version",
        "template_version",
        ["id", "template_id", "version", "spec", "note", "created_by", "created_at", "updated_at"],
        lambda r: (
            uid("template_version", r["id"]), uid("template", r["template_id"]),
            r["version"] or 1, js(r["spec"], {}), txt(r["note"]),
            uid("accounts_user", r["created_by_id"]) if r["created_by_id"] else None,
            r["created_at"], r["updated_at"],
        ),
        conflict_cols="template_id, version",
    )
    run(
        dst_conn, src_cur, "template_withdrawal_request -> template_withdrawal_request",
        "SELECT * FROM template_withdrawal_request",
        "template_withdrawal_request",
        ["id", "user_id", "amount", "card_number", "card_owner", "status",
         "rejection_reason", "reviewed_by", "reviewed_at", "created_at", "updated_at"],
        lambda r: (
            uid("template_withdrawal_request", r["id"]), uid("accounts_user", r["user_id"]),
            dec(r["amount"]), txt(r["card_number"]), txt(r["card_owner"]),
            txt(r["status"], "pending"), r["rejection_reason"],
            uid("accounts_user", r["reviewed_by_id"]) if r["reviewed_by_id"] else None,
            r["reviewed_at"], r["created_at"], r["updated_at"],
        ),
    )

    # ---- 13. collection, field ----
    seen_slugs = {}

    def transform_collection(r):
        base_slug = slugify_collection(r["name"], r["id"])
        key = (r["project_id"], base_slug)
        n = seen_slugs.get(key, 0)
        seen_slugs[key] = n + 1
        slug = base_slug if n == 0 else f"{base_slug}_{n + 1}"
        return (
            uid("collection", r["id"]), uid("project", r["project_id"]), txt(r["name"]),
            slug, r["created_at"], r["updated_at"],
        )

    run(
        dst_conn, src_cur, "collection -> collection",
        "SELECT * FROM collection",
        "collection",
        ["id", "project_id", "name", "slug", "created_at", "updated_at"],
        transform_collection,
    )
    run(
        dst_conn, src_cur, "field -> field",
        "SELECT * FROM field",
        "field",
        ["id", "collection_id", "name", "type", "required", "default_value",
         "relation_collection_id", "created_at", "updated_at"],
        lambda r: (
            uid("field", r["id"]), uid("collection", r["collection_id"]), txt(r["name"]),
            map_field_type(r["type"]), bool(r["required"]),
            field_default_value(r["default"]),
            uid("collection", r["relation_collection_id"]) if r["relation_collection_id"] else None,
            r["created_at"], r["updated_at"],
        ),
    )
    SKIPPED_NOTES.append("CMS documents live in MongoDB (not in this Postgres dump) — not migrated by this script.")
    SKIPPED_NOTES.append("export_task (0 rows in source) — no-op, cms_export_task left empty.")

    # ---- 14. authbot ----
    run(
        dst_conn, src_cur, "authbot_botuser -> authbot_bot_users",
        "SELECT * FROM authbot_botuser",
        "authbot_bot_users",
        ["id", "telegram_id", "phone", "first_name", "last_name", "username",
         "created_at", "updated_at"],
        lambda r: (
            uid("authbot_botuser", r["id"]), r["user_id"], txt(r["phone"]),
            r["first_name"], r["last_name"], r["username"], r["created_at"], r["updated_at"],
        ),
        conflict_cols="telegram_id, phone",
    )
    run(
        dst_conn, src_cur, "authbot_code -> authbot_codes",
        "SELECT * FROM authbot_code",
        "authbot_codes",
        ["id", "code", "message_id", "bot_user_id", "expires_at", "created_at", "updated_at"],
        lambda r: (
            uid("authbot_code", r["id"]), str(r["code"]), r["message_id"],
            uid("authbot_botuser", r["user_id"]), r["created_at"],
            r["created_at"], r["updated_at"],
        ),
        conflict_cols="code",
    )
    SKIPPED_NOTES.append(
        "authbot_codes.expires_at did not exist in Django — synthesised as created_at "
        "(source rows are historical/already-used login codes, not functionally important)."
    )

    # ---- 15. accounts_userverification -> user_verification ----
    run(
        dst_conn, src_cur, "user_verification -> user_verification",
        "SELECT * FROM user_verification",
        "user_verification",
        ["id", "user_id", "full_name", "passport_image", "selfie_image",
         "residence_reference", "status", "rejection_reason", "reviewed_by",
         "reviewed_at", "created_at", "updated_at"],
        lambda r: (
            uid("user_verification", r["id"]), uid("accounts_user", r["user_id"]),
            txt(r["full_name"]), txt(r["passport_image"]), txt(r["selfie_image"]),
            txt(r["residence_reference"]), txt(r["status"], "pending"),
            r["rejection_reason"],
            uid("accounts_user", r["reviewed_by_id"]) if r["reviewed_by_id"] else None,
            r["reviewed_at"], r["created_at"], r["updated_at"],
        ),
    )

    # ---- 16. admin_audit_log -> audit_log ----
    run(
        dst_conn, src_cur, "admin_audit_log -> audit_log",
        "SELECT * FROM admin_audit_log",
        "audit_log",
        ["id", "actor_id", "action", "target", "metadata", "ip", "created_at"],
        lambda r: (
            uid("admin_audit_log", r["id"]),
            uid("accounts_user", r["actor_id"]) if r["actor_id"] else None,
            txt(r["action"]),
            f"{txt(r['target_type'])}:{txt(r['target_id'])}".strip(":"),
            js({"payload_before": r["payload_before"], "payload_after": r["payload_after"],
                "note": r["note"]}, {}),
            str(r["ip_address"]) if r["ip_address"] else None,
            r["created_at"],
        ),
    )

    # ---- Admin login setup ----
    print("\n== Admin login setup ==")
    admin_hash = bcrypt.hashpw(b"admin12345", bcrypt.gensalt()).decode()
    with dst_conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as c:
        c.execute("SELECT id, phone FROM users WHERE is_superuser = TRUE ORDER BY created_at LIMIT 1")
        admin = c.fetchone()
        if not admin:
            c.execute("SELECT id, phone FROM users ORDER BY created_at LIMIT 1")
            admin = c.fetchone()
            if admin:
                with dst_conn.cursor() as c2:
                    c2.execute(
                        "UPDATE users SET is_superuser = TRUE, is_staff = TRUE WHERE id = %s",
                        (admin["id"],),
                    )
        if admin:
            with dst_conn.cursor() as c2:
                c2.execute("UPDATE users SET password_hash = %s WHERE id = %s", (admin_hash, admin["id"]))
            dst_conn.commit()
            print(f"Admin ready: phone={admin['phone']!r} password=admin12345")
        else:
            print("No users found at all — could not set up an admin login.")
            dst_conn.commit()

    # ---- Verify: source vs target counts ----
    print("\n== Verification: source vs target counts ==")
    pairs = [
        ("plan", "plan"), ("accounts_user", "users"), ("user_subscription", "user_subscription"),
        ("project", "projects"), ("relase", "relase"), ("debug", "debug"),
        ("enviroment", "enviroment"), ("file", "file"), ("resource", "resource"),
        ("api_projectresourcesnapshot", "project_resource_snapshot"),
        ("api_debugrunlog", "debug_run_log"), ("message", "message"), ("module", "module"),
        ("project_module", "project_module"), ("webhook_endpoint", "webhook_endpoint"),
        ("managed_bot", "managed_bot"), ("credential", "credential"),
        ("transaction", "transaction"), ("payment_order", "payment_order"),
        ("payment_withdrawal_request", "payment_withdrawal_request"), ("history", "history"),
        ("template_category", "template_category"), ("template_feature", "template_feature"),
        ("template", "template"), ("template_categories", "template_category_map"),
        ("template_features", "template_feature_map"), ("template_like", "template_like"),
        ("usertemplate", "usertemplate"), ("templae_order", "template_order"),
        ("template_submission_request", "template_submission_request"),
        ("template_submission_request_categories", "template_submission_category_map"),
        ("template_submission_request_features", "template_submission_feature_map"),
        ("template_version", "template_version"),
        ("template_withdrawal_request", "template_withdrawal_request"),
        ("collection", "collection"), ("field", "field"),
        ("authbot_botuser", "authbot_bot_users"), ("authbot_code", "authbot_codes"),
        ("user_verification", "user_verification"), ("admin_audit_log", "audit_log"),
    ]
    with dst_conn.cursor() as dc:
        print(f"{'source':40s} {'target':30s} {'src':>8s} {'dst':>8s}")
        for src_t, dst_t in pairs:
            src_cur.execute(f"SELECT count(*) AS c FROM {src_t}")
            src_n = src_cur.fetchone()["c"]
            dc.execute(f"SELECT count(*) FROM {dst_t}")
            dst_n = dc.fetchone()[0]
            flag = "" if src_n == dst_n else "  <-- MISMATCH"
            print(f"{src_t:40s} {dst_t:30s} {src_n:8d} {dst_n:8d}{flag}")

    print("\n== Skipped / coerced ==")
    for n in SKIPPED_NOTES:
        print(f" - {n}")

    src_cur.close()
    src_conn.close()
    dst_conn.close()


def slugify_collection(name, fallback_id):
    import re
    s = (name or "").strip().lower()
    s = re.sub(r"[^a-z0-9]+", "_", s).strip("_")
    if not s:
        s = f"collection_{fallback_id}"
    return s


if __name__ == "__main__":
    main()
