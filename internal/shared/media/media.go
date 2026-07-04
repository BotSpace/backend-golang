// Package media turns object-storage keys stored in the DB (e.g.
// "template/cover_107.png") into full public URLs. Files live in a PUBLIC
// Backblaze B2 (S3-compatible) bucket — verified reachable at
// <STORAGE_URL>/<bucket>/<key> with no auth — so this is plain URL string
// building, NOT a signed-request client. Django's S3Boto3Storage did the same
// via `image.url`; the DB only ever holds the bucket-relative key.
package media

import "strings"

var (
	baseURL string // S3 endpoint, e.g. "https://s3.us-east-005.backblazeb2.com"
	bucket  string // public media bucket, e.g. "botspace"
)

// Configure sets the public storage base. Call once at startup from config
// (APP_STORAGE_URL / APP_STORAGE_BUCKET_MEDIA). Both empty ⇒ URL is a no-op
// pass-through, so an unconfigured backend returns raw keys instead of broken
// URLs.
func Configure(storageURL, mediaBucket string) {
	baseURL = strings.TrimRight(storageURL, "/")
	bucket = strings.Trim(mediaBucket, "/")
}

// URL turns a stored object key into its full public URL. Empty keys and
// already-absolute URLs (http/https) pass through unchanged, so it's safe to
// wrap every file field and idempotent if a value is already a URL.
func URL(key string) string {
	if key == "" || strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		return key
	}
	if baseURL == "" || bucket == "" {
		return key
	}
	return baseURL + "/" + bucket + "/" + strings.TrimLeft(key, "/")
}
