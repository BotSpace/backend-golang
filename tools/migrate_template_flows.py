#!/usr/bin/env python3
"""Migrate linked template projects to the canonical runtime flow contract.

Dry-run against an exported internal template feed:
  python tools/migrate_template_flows.py --input templates.json --output migrated.json

Production apply requires BACKEND_BASE_URL, BACKEND_INTERNAL_TOKEN and
ADMIN_USER_ID. Every project is fetched as its owner before a scoped PATCH.
"""

from __future__ import annotations

import argparse
import copy
import difflib
import hashlib
import json
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any, Iterable


STATE_PLACEHOLDER = re.compile(r"\{\{\s*state_([A-Za-z_]\w*(?:\.[^{}\s]+)?)\s*\}\}")
STATE_IDENTIFIER = re.compile(r"\bstate_([A-Za-z_]\w*)\b")
CONTEXT_STATE = re.compile(r"context\[['\"]state_([A-Za-z_]\w*)['\"]\]")
STATE_HELPER_KEY = re.compile(
    r"\b((?:get|set|has|delete)_(?:context|state)\(\s*['\"])state_"
)
CONTEXT_SET_LINE = re.compile(
    r"(?m)^([ \t]*)context\.set\(['\"](?:state_)?([A-Za-z_]\w*)['\"]\s*,\s*(.+)\);\s*$"
)


def _buttons(node: dict[str, Any]) -> Iterable[dict[str, Any]]:
    data = node.get("data") if isinstance(node.get("data"), dict) else {}
    for row in data.get("rows") or []:
        if not isinstance(row, dict):
            continue
        for button in row.get("columns") or []:
            if isinstance(button, dict):
                yield button


def _button_handle(node_id: str, button: dict[str, Any]) -> str:
    return f"target-handler-{node_id}-{button.get('text') or ''}"


def _replace_placeholders(value: Any, stats: Counter[str]) -> Any:
    if isinstance(value, str):
        replaced, count = STATE_PLACEHOLDER.subn(r"{{\1}}", value)
        stats["state_placeholders"] += count
        return replaced
    if isinstance(value, list):
        return [_replace_placeholders(item, stats) for item in value]
    if isinstance(value, dict):
        return {key: _replace_placeholders(item, stats) for key, item in value.items()}
    return value


def _canonical_custom_code(code: str, *, replace_bare: bool = False) -> tuple[str, int]:
    code, helper_count = STATE_HELPER_KEY.subn(r"\1", code)
    code, context_count = CONTEXT_STATE.subn(r"vars.\1", code)
    code = CONTEXT_SET_LINE.sub(r"\1vars.\2 = \3;", code)
    if replace_bare:
        code = STATE_IDENTIFIER.sub(r"vars.\1", code)
    return code, helper_count + context_count


def _root_callback_matchers(flow: dict[str, Any]) -> list[tuple[str, str]]:
    incoming = {edge.get("target") for edge in flow.get("edges") or []}
    result: list[tuple[str, str]] = []
    for node in flow.get("nodes") or []:
        if node.get("type") != "CallbackQueryTriggerNode":
            continue
        data = node.get("data") or {}
        if not data.get("global") and node.get("id") in incoming:
            continue
        context = data.get("context") if isinstance(data.get("context"), dict) else {}
        result.append((str(data.get("filter") or "any").lower(), str(context.get("value") or "")))
    return result


def _callback_matches(value: str, matchers: list[tuple[str, str]]) -> bool:
    for operation, expected in matchers:
        if operation == "any":
            return True
        if operation == "equals" and value == expected:
            return True
        if operation in {"starts_with", "prefix"} and value.startswith(expected):
            return True
        if operation == "ends_with" and value.endswith(expected):
            return True
        if operation == "contains" and expected in value:
            return True
    return False


def _restore_duplicate_menu_edges(flow: dict[str, Any], stats: Counter[str]) -> None:
    nodes = {node.get("id"): node for node in flow.get("nodes") or []}
    edges = flow.setdefault("edges", [])
    routes: dict[str, set[str]] = defaultdict(set)
    existing = {(edge.get("source"), edge.get("sourceHandle")) for edge in edges}

    for edge in edges:
        node = nodes.get(edge.get("source"))
        if not node:
            continue
        for button in _buttons(node):
            if edge.get("sourceHandle") != _button_handle(str(node.get("id")), button):
                continue
            value = str(button.get("value") or button.get("text") or "")
            if value:
                routes[value].add(str(edge.get("target")))

    matchers = _root_callback_matchers(flow)
    for node in list(nodes.values()):
        data = node.get("data") or {}
        if str(data.get("keyboard_kind") or "").lower() != "inline":
            continue
        for button in _buttons(node):
            if str(button.get("type") or "callback").lower() not in {"", "callback"}:
                continue
            handle = _button_handle(str(node.get("id")), button)
            if (node.get("id"), handle) in existing:
                continue
            value = str(button.get("value") or button.get("text") or "")
            if _callback_matches(value, matchers):
                continue
            targets = routes.get(value) or set()
            if len(targets) != 1:
                continue
            target = next(iter(targets))
            digest = hashlib.sha1(f"{node.get('id')}:{handle}:{target}".encode()).hexdigest()[:12]
            edges.append({
                "id": f"template-migration-{digest}",
                "type": "smart-edge",
                "source": node.get("id"),
                "target": target,
                "sourceHandle": handle,
                "targetHandle": "target-handler",
            })
            existing.add((node.get("id"), handle))
            stats["restored_menu_edges"] += 1


def _repair_stale_button_edges(flow: dict[str, Any], stats: Counter[str]) -> None:
    nodes = {node.get("id"): node for node in flow.get("nodes") or []}
    edges = flow.get("edges") or []
    used = {(edge.get("source"), edge.get("sourceHandle")) for edge in edges}
    callback_targets: dict[str, set[str]] = defaultdict(set)
    for trigger in nodes.values():
        if trigger.get("type") != "CallbackQueryTriggerNode":
            continue
        data = trigger.get("data") or {}
        context = data.get("context") if isinstance(data.get("context"), dict) else {}
        if str(data.get("filter") or "").lower() != "equals" or not context.get("value"):
            continue
        for edge in edges:
            if edge.get("source") == trigger.get("id"):
                callback_targets[str(context["value"]).lower()].add(str(edge.get("target")))

    remove_ids: set[str] = set()
    for edge in edges:
        node = nodes.get(edge.get("source"))
        handle = str(edge.get("sourceHandle") or "")
        if not node or not handle.startswith(f"target-handler-{node.get('id')}-"):
            continue
        buttons = list(_buttons(node))
        valid = {_button_handle(str(node.get("id")), button) for button in buttons}
        if handle in valid:
            continue
        if not buttons:
            remove_ids.add(str(edge.get("id")))
            stats["removed_dead_button_edges"] += 1
            continue

        old_text = handle.removeprefix(f"target-handler-{node.get('id')}-").lower()
        candidates: list[tuple[float, dict[str, Any]]] = []
        for button in buttons:
            candidate_handle = _button_handle(str(node.get("id")), button)
            if (node.get("id"), candidate_handle) in used:
                continue
            text = str(button.get("text") or "").lower()
            value = str(button.get("value") or "").lower()
            score = difflib.SequenceMatcher(None, old_text, text).ratio()
            edge_tokens = set(re.findall(r"[a-z]+", str(edge.get("id") or "").lower()))
            value_tokens = set(re.findall(r"[a-z]+", value))
            if edge_tokens & value_tokens:
                score += 1.5
            if str(edge.get("target")) in callback_targets.get(value, set()):
                score += 2.0
            candidates.append((score, button))
        candidates.sort(key=lambda item: item[0], reverse=True)
        if not candidates or candidates[0][0] < 1.25:
            remove_ids.add(str(edge.get("id")))
            stats["removed_dead_button_edges"] += 1
            continue
        if len(candidates) > 1 and candidates[0][0] == candidates[1][0]:
            continue
        new_handle = _button_handle(str(node.get("id")), candidates[0][1])
        edge["sourceHandle"] = new_handle
        used.add((node.get("id"), new_handle))
        stats["repaired_button_handles"] += 1

    if remove_ids:
        flow["edges"] = [edge for edge in edges if str(edge.get("id")) not in remove_ids]


def _scope_callback_capture_edges(flow: dict[str, Any], stats: Counter[str]) -> None:
    """Bind old base edges to the exact inline button they represent."""
    nodes = {node.get("id"): node for node in flow.get("nodes") or []}
    edges = flow.get("edges") or []
    exact = {
        (edge.get("source"), edge.get("target"), edge.get("sourceHandle"))
        for edge in edges
        if edge.get("sourceHandle") not in {None, "", "source-handler"}
    }
    additions: list[dict[str, Any]] = []
    removals: set[str] = set()
    for edge in list(edges):
        if edge.get("sourceHandle") not in {None, "", "source-handler"}:
            continue
        source = nodes.get(edge.get("source"))
        target = nodes.get(edge.get("target"))
        if not source or not target or target.get("type") != "CallbackQueryTriggerNode":
            continue
        data = target.get("data") or {}
        context = data.get("context") if isinstance(data.get("context"), dict) else {}
        expected = str(context.get("value") or "")
        operation = str(data.get("filter") or "any").lower()
        if operation != "any" and not expected:
            continue
        matches = [
            button for button in _buttons(source)
            if _callback_matches(
                str(button.get("value") or button.get("text") or ""),
                [(operation, expected)],
            )
        ]
        if not matches:
            continue
        available = []
        for button in matches:
            handle = _button_handle(str(source.get("id")), button)
            key = (edge.get("source"), edge.get("target"), handle)
            if key not in exact:
                available.append(handle)
                exact.add(key)
        if not available:
            removals.add(str(edge.get("id")))
            stats["removed_duplicate_base_edges"] += 1
            continue
        edge["sourceHandle"] = available[0]
        stats["scoped_callback_edges"] += 1
        for handle in available[1:]:
            clone = copy.deepcopy(edge)
            digest = hashlib.sha1(
                f"{edge.get('id')}:{handle}".encode()
            ).hexdigest()[:12]
            clone["id"] = f"template-callback-{digest}"
            clone["sourceHandle"] = handle
            additions.append(clone)
            stats["scoped_callback_edges"] += 1
    if removals:
        flow["edges"] = [edge for edge in edges if str(edge.get("id")) not in removals]
    flow.setdefault("edges", []).extend(additions)


def _scope_single_screen_root_callbacks(flow: dict[str, Any], stats: Counter[str]) -> None:
    nodes = {node.get("id"): node for node in flow.get("nodes") or []}
    edges = flow.setdefault("edges", [])
    owners: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]] = defaultdict(list)
    for node in nodes.values():
        data = node.get("data") or {}
        if str(data.get("keyboard_kind") or "").lower() != "inline":
            continue
        for button in _buttons(node):
            if str(button.get("type") or "callback").lower() not in {"", "callback"}:
                continue
            value = str(button.get("value") or button.get("text") or "")
            if value:
                owners[value].append((node, button))

    existing = {(e.get("source"), e.get("target"), e.get("sourceHandle")) for e in edges}
    for trigger in nodes.values():
        if trigger.get("type") != "CallbackQueryTriggerNode":
            continue
        data = trigger.get("data") or {}
        context = data.get("context") if isinstance(data.get("context"), dict) else {}
        if str(data.get("filter") or "").lower() != "equals":
            continue
        value = str(context.get("value") or "")
        matches = owners.get(value) or []
        owner_ids = {node.get("id") for node, _button in matches}
        if len(owner_ids) != 1 or not matches:
            continue
        owner, button = matches[0]
        handle = _button_handle(str(owner.get("id")), button)
        key = (owner.get("id"), trigger.get("id"), handle)
        if key not in existing:
            digest = hashlib.sha1(f"{key}".encode()).hexdigest()[:12]
            edges.append({
                "id": f"template-screen-callback-{digest}",
                "type": "smart-edge",
                "source": owner.get("id"),
                "target": trigger.get("id"),
                "sourceHandle": handle,
                "targetHandle": "target-handler",
            })
            existing.add(key)
        data["global"] = False
        stats["scoped_root_callbacks"] += 1


def _remove_unroutable_callback_buttons(flow: dict[str, Any], stats: Counter[str]) -> None:
    routed = {(edge.get("source"), edge.get("sourceHandle")) for edge in flow.get("edges") or []}
    matchers = _root_callback_matchers(flow)
    for node in flow.get("nodes") or []:
        data = node.get("data") or {}
        if str(data.get("keyboard_kind") or "").lower() != "inline":
            continue
        for row in data.get("rows") or []:
            if not isinstance(row, dict) or not isinstance(row.get("columns"), list):
                continue
            kept = []
            for button in row["columns"]:
                if button.get("collectionData") or not button.get("text"):
                    kept.append(button)
                    continue
                button_type = str(button.get("type") or "callback").lower()
                if button_type not in {"", "callback"}:
                    kept.append(button)
                    continue
                handle = _button_handle(str(node.get("id")), button)
                value = str(button.get("value") or button.get("text") or "")
                if (node.get("id"), handle) in routed or _callback_matches(value, matchers):
                    kept.append(button)
                else:
                    stats["removed_dead_buttons"] += 1
            row["columns"] = kept
        data["rows"] = [row for row in data.get("rows") or [] if row.get("columns")]


def _mark_terminal_custom_nodes(flow: dict[str, Any], stats: Counter[str]) -> None:
    routed_outputs = {
        edge.get("source")
        for edge in flow.get("edges") or []
        if str(edge.get("sourceHandle") or "").startswith("output-")
    }
    for node in flow.get("nodes") or []:
        data = node.get("data") or {}
        if node.get("type") != "CustomCodeNode":
            continue
        outputs = [
            str(output.get("name"))
            for output in data.get("outputs") or []
            if isinstance(output, dict) and output.get("name")
        ]
        used = {
            str(edge.get("sourceHandle") or "").removeprefix("output-")
            for edge in flow.get("edges") or []
            if edge.get("source") == node.get("id")
            and str(edge.get("sourceHandle") or "").startswith("output-")
        }
        code = str(data.get("jsCode") or "")
        terminal_outputs = [
            name for name in outputs
            if name not in used
            and re.search(rf"\bexit\(['\"]{re.escape(name)}['\"]\)", code)
            and any(token in code for token in (
                "send_message", "edit_or_send", "edit_message_text",
                "send_photo", "answer_callback_query",
            ))
        ]
        if terminal_outputs:
            data["terminalOutputs"] = terminal_outputs
            stats["terminal_custom_outputs"] += len(terminal_outputs)
        if outputs and node.get("id") not in routed_outputs and not data.get("terminal"):
            data["terminal"] = True
            stats["terminal_custom_nodes"] += 1


def _deduplicate_edges(flow: dict[str, Any], stats: Counter[str]) -> None:
    seen: set[tuple[Any, Any, Any, Any]] = set()
    kept = []
    for edge in flow.get("edges") or []:
        key = (
            edge.get("source"), edge.get("target"),
            edge.get("sourceHandle"), edge.get("targetHandle"),
        )
        if key in seen:
            stats["removed_duplicate_edges"] += 1
            continue
        seen.add(key)
        kept.append(edge)
    flow["edges"] = kept


def _remove_isolated_action_nodes(flow: dict[str, Any], stats: Counter[str]) -> None:
    connected = {
        endpoint
        for edge in flow.get("edges") or []
        for endpoint in (edge.get("source"), edge.get("target"))
    }
    kept = []
    for node in flow.get("nodes") or []:
        if node.get("id") not in connected and "Trigger" not in str(node.get("type") or ""):
            stats["removed_isolated_nodes"] += 1
            continue
        kept.append(node)
    flow["nodes"] = kept


def migrate_flow(original: dict[str, Any]) -> tuple[dict[str, Any], Counter[str]]:
    stats: Counter[str] = Counter()
    flow = _replace_placeholders(copy.deepcopy(original), stats)

    custom_ids: dict[str, list[str]] = {}
    for node in flow.get("nodes") or []:
        data = node.setdefault("data", {})
        keyboard = data.get("keyboard")
        if isinstance(keyboard, dict) and keyboard.get("active") in {"inline", "reply"}:
            kind = str(keyboard["active"])
            raw_rows = keyboard.get(kind) or []
            data["keyboard_kind"] = kind
            data["rows"] = [
                {"columns": row}
                for row in raw_rows
                if isinstance(row, list)
            ]
            del data["keyboard"]
            stats["legacy_keyboards"] += 1
        state = data.get("state")
        if isinstance(state, dict) and state.get("key"):
            data["state_key"] = state.get("key")
            data["state_type"] = state.get("type")
            if "value" in state:
                data["state_value"] = state.get("value")
            del data["state"]
            stats["flat_capture_config"] += 1
        elif isinstance(state, dict):
            del data["state"]
            stats["removed_empty_capture_config"] += 1
        if "save_state" in data:
            del data["save_state"]
            stats["removed_save_state"] += 1

        for button in _buttons(node):
            button_type = str(button.get("type") or "").lower()
            if button_type in {"contact", "location"}:
                button["type"] = f"request_{button_type}"
                stats["native_button_types"] += 1

        if node.get("type") == "CustomNode":
            code, state_key_count = _canonical_custom_code(
                str(data.get("code") or ""), replace_bare=True
            )
            outputs = list(dict.fromkeys(re.findall(r"\bexit\(['\"]([^'\"]+)['\"]\)", code)))
            node["type"] = "CustomCodeNode"
            node["data"] = {"jsCode": code, "outputs": [{"name": name} for name in outputs]}
            custom_ids[str(node.get("id"))] = outputs
            stats["custom_nodes"] += 1
            stats["custom_state_keys"] += state_key_count
        elif node.get("type") == "CustomCodeNode" and isinstance(data.get("jsCode"), str):
            code, state_key_count = _canonical_custom_code(data["jsCode"])
            if code != data["jsCode"]:
                data["jsCode"] = code
                stats["custom_state_keys"] += state_key_count
            custom_ids[str(node.get("id"))] = [
                str(output.get("name"))
                for output in data.get("outputs") or []
                if isinstance(output, dict) and output.get("name")
            ]

        if node.get("type") == "HTTPRequestNode" and not data.get("responseVariable"):
            data["responseVariable"] = "http_response"
            stats["http_response_variables"] += 1
        if node.get("type") == "AIKnowledgeToolNode" and not data.get("description"):
            data["description"] = "Template bilim bazasidan tegishli ma'lumotni qidiradi."
            stats["knowledge_descriptions"] += 1

    for edge in flow.get("edges") or []:
        handle = edge.get("sourceHandle")
        if handle == "source-handler-true":
            edge["sourceHandle"] = "true"
            stats["condition_handles"] += 1
        elif handle == "source-handler-false":
            edge["sourceHandle"] = "false"
            stats["condition_handles"] += 1
        outputs = custom_ids.get(str(edge.get("source")))
        if outputs:
            raw = str(edge.get("sourceHandle") or "")
            name = raw.removeprefix("output-")
            if name not in outputs and len(outputs) == 1:
                name = outputs[0]
            if name in outputs:
                edge["sourceHandle"] = f"output-{name}"
                stats["custom_output_handles"] += 1

    _scope_callback_capture_edges(flow, stats)
    _scope_single_screen_root_callbacks(flow, stats)
    _repair_stale_button_edges(flow, stats)
    _restore_duplicate_menu_edges(flow, stats)
    _remove_unroutable_callback_buttons(flow, stats)
    _deduplicate_edges(flow, stats)
    _mark_terminal_custom_nodes(flow, stats)
    _remove_isolated_action_nodes(flow, stats)
    return flow, stats


class API:
    def __init__(self) -> None:
        self.base = os.environ["BACKEND_BASE_URL"].rstrip("/")
        self.token = os.environ["BACKEND_INTERNAL_TOKEN"]
        self.admin_id = os.environ["ADMIN_USER_ID"]

    def request(self, method: str, path: str, *, user_id: str | None = None,
                role: str = "user", body: Any = None) -> Any:
        headers = {"X-Internal-Token": self.token, "X-User-Id": user_id or self.admin_id,
                   "X-User-Role": role}
        payload = None
        if body is not None:
            payload = json.dumps(body, ensure_ascii=False).encode()
            headers["Content-Type"] = "application/json"
        for attempt in range(6):
            req = urllib.request.Request(self.base + path, data=payload, headers=headers, method=method)
            try:
                with urllib.request.urlopen(req, timeout=40) as response:
                    result = json.load(response)
                time.sleep(0.08)
                return result.get("data", result) if isinstance(result, dict) else result
            except urllib.error.HTTPError as exc:
                if exc.code != 429 or attempt == 5:
                    raise RuntimeError(f"{method} {path}: HTTP {exc.code}: {exc.read().decode()}") from exc
                time.sleep(1 + attempt)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--backup", type=Path, default=Path("/tmp/botspace-template-flow-backup.json"))
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()

    api = API() if args.apply else None
    if args.input:
        payload = json.loads(args.input.read_text())
        rows = payload.get("results", [])
    elif api:
        listing = api.request("GET", "/api/v1/template/internal/list?limit=500", role="superuser")
        rows = listing.get("results", listing)
    else:
        parser.error("--input or --apply is required")

    totals: Counter[str] = Counter()
    changed: list[dict[str, Any]] = []
    output_rows = []
    for row in rows:
        migrated, stats = migrate_flow(row.get("flow") or {})
        out = copy.deepcopy(row)
        out["flow"] = migrated
        output_rows.append(out)
        if migrated != (row.get("flow") or {}):
            totals.update(stats)
            changed.append({"id": row.get("id"), "slug": row.get("slug"), "stats": dict(stats)})

    if args.output:
        args.output.write_text(json.dumps({"results": output_rows}, ensure_ascii=False))

    if args.apply and api:
        backups = []
        for item in changed:
            detail = api.request("GET", f"/api/v1/admin/templates/{item['id']}", role="superuser")
            project_id, owner_id = detail.get("project_id"), detail.get("author_id")
            if not project_id or not owner_id:
                raise RuntimeError(f"template {item['slug']} has no linked project/author")
            project = api.request("GET", f"/api/v1/projects/{project_id}", user_id=owner_id)
            current = project.get("flow") or {}
            migrated, _ = migrate_flow(current)
            if migrated == current:
                continue
            backups.append({"template_id": item["id"], "slug": item["slug"],
                            "project_id": project_id, "owner_id": owner_id, "flow": current})
            args.backup.write_text(json.dumps(backups, ensure_ascii=False))
            api.request("PATCH", f"/api/v1/projects/{project_id}", user_id=owner_id,
                        body={"flow": migrated})

    print(json.dumps({"templates": len(rows), "changed": len(changed),
                      "totals": dict(totals), "details": changed}, ensure_ascii=False))


if __name__ == "__main__":
    main()
