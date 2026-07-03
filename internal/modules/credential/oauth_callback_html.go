package credential

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"

	"github.com/gin-gonic/gin"
)

// This file ports core/apps/credentials/views/oauth.py's _SUCCESS_HTML /
// _ERROR_HTML byte-for-byte (same markup, same window.opener postMessage
// contract) so the dashboard's existing popup-window listener — which
// already worked against Django's callback — keeps working unchanged now
// that the callback is served by this backend instead.

const oauthSuccessHTMLTmpl = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Connected</title></head>
<body>
<p>Connected! This window will close automatically.</p>
<p>If it doesn't, <a href="%s">return to the panel</a>.</p>
<script>
try {
  window.opener.postMessage({type:'credential-oauth',ok:true,provider:%s}, '*');
  window.close();
} catch(e) {}
</script>
</body>
</html>`

const oauthErrorHTMLTmpl = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Error</title></head>
<body>
<p>Authentication failed: %s</p>
<p><a href="%s">Return to the panel</a>.</p>
<script>
try {
  window.opener.postMessage({type:'credential-oauth',ok:false,error:%s,provider:%s}, '*');
  window.close();
} catch(e) {}
</script>
</body>
</html>`

// writeOAuthSuccessHTML writes the 200 HTML page a successful OAuthCallback
// returns. provider/panelURL are HTML-escaped for the visible link and
// JSON-encoded for the embedded postMessage script (provider is a URL path
// segment an attacker controls, so it is never trusted verbatim in either
// context).
func writeOAuthSuccessHTML(c *gin.Context, provider, panelURL string) {
	providerJS, _ := json.Marshal(provider)
	body := fmt.Sprintf(oauthSuccessHTMLTmpl, html.EscapeString(panelURL), providerJS)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(body))
}

// writeOAuthErrorHTML writes the 400 HTML page an OAuthCallback failure
// returns — mirrors Django's _error_html (also always HTTP 400, even for a
// server-side failure like a token-exchange error: the browser popup only
// ever expects a leaf page here, never a raw 5xx).
func writeOAuthErrorHTML(c *gin.Context, provider, panelURL, message string) {
	providerJS, _ := json.Marshal(provider)
	messageJS, _ := json.Marshal(message)
	body := fmt.Sprintf(oauthErrorHTMLTmpl, html.EscapeString(message), html.EscapeString(panelURL), messageJS, providerJS)
	c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(body))
}
