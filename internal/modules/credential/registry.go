package credential

// Field describes one input of a credential type — the Go counterpart of an
// entry in Django's registry.py CREDENTIAL_TYPES[i]["fields"].
type Field struct {
	Name     string        `json:"name"`
	Label    string        `json:"label"`
	Type     string        `json:"type"` // text|password|number|textarea|select|url|switch
	Secret   bool          `json:"secret,omitempty"`
	Required bool          `json:"required,omitempty"`
	Default  any           `json:"default,omitempty"`
	Options  []FieldOption `json:"options,omitempty"`
}

// FieldOption is one choice of a "select" field.
type FieldOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// TypeDef is a credential type — purpose-specific (a service), not a generic
// auth scheme. Mirrors one entry of Django's CREDENTIAL_TYPES.
type TypeDef struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Icon     string   `json:"icon"`
	Color    string   `json:"color,omitempty"`
	Mode     string   `json:"mode"` // bearer|apikey|basic|header|oauth2|none
	Fields   []Field  `json:"fields"`
	Provider string   `json:"provider,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	Source   string   `json:"source"`   // always "code" — admin/module sources are not ported
	Editable bool     `json:"editable"` // always false — code types are immutable
}

// Modes accepted by a type definition. Kept for future validation of
// admin-managed types, should that ever be ported.
const (
	ModeBearer = "bearer"
	ModeAPIKey = "apikey"
	ModeBasic  = "basic"
	ModeHeader = "header"
	ModeOAuth2 = "oauth2"
	ModeNone   = "none"
)

// Types is the code-defined credential type registry — the Go port of
// registry.py's CREDENTIAL_TYPES. Django also layers admin (DB Config) and
// module (manifest_cache) sources on top; those are not ported here, only
// the code source, which is what the engine and constructor rely on today.
var Types = []TypeDef{
	// --- AI providers ---
	{
		Key: "openai", Label: "ChatGPT (OpenAI)", Icon: "bot", Color: "#10A37F", Mode: ModeBearer,
		Fields: []Field{
			{Name: "api_key", Label: "API key", Type: "password", Secret: true, Required: true},
			{Name: "base_url", Label: "Base URL", Type: "text", Default: "https://api.openai.com/v1"},
		},
	},
	{
		Key: "ai_studio", Label: "Google AI Studio", Icon: "sparkles", Color: "#1A73E8", Mode: ModeBearer,
		Fields: []Field{
			{Name: "api_key", Label: "API key", Type: "password", Secret: true, Required: true},
		},
	},
	{
		Key: "ollama", Label: "Ollama", Icon: "boxes", Color: "#F97316", Mode: ModeBearer,
		Fields: []Field{
			{Name: "base_url", Label: "Base URL", Type: "text", Default: "https://ollama.com/v1"},
			{Name: "api_key", Label: "API key", Type: "password", Secret: true},
		},
	},
	// --- Messaging ---
	{
		Key: "telegram", Label: "Telegram", Icon: "send", Color: "#229ED9", Mode: ModeNone,
		Fields: []Field{
			{Name: "bot_token", Label: "Bot token", Type: "password", Secret: true, Required: true},
		},
	},
	// --- OAuth2 types (Google) — fields are populated by the OAuth callback,
	// not by this form; the OAuth flow itself is not ported (out of scope). ---
	{
		Key: "google_email", Label: "Google (Email)", Icon: "mail", Color: "#EA4335", Mode: ModeOAuth2,
		Provider: "google",
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Fields: []Field{},
	},
	{
		Key: "google_sheets", Label: "Google Sheets", Icon: "table", Color: "#0F9D58", Mode: ModeOAuth2,
		Provider: "google",
		Scopes: []string{
			"openid", "email",
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive.readonly",
		},
		Fields: []Field{},
	},
	{
		Key: "google_drive", Label: "Google Drive", Icon: "folder", Color: "#FBBC04", Mode: ModeOAuth2,
		Provider: "google",
		Scopes: []string{
			"openid", "email",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive.readonly",
		},
		Fields: []Field{},
	},
}

var typeIndex = buildTypeIndex()

func buildTypeIndex() map[string]TypeDef {
	m := make(map[string]TypeDef, len(Types))
	for _, t := range Types {
		t.Source = "code"
		t.Editable = false
		m[t.Key] = t
	}
	return m
}

// GetType resolves a type_key against the code registry, or (false) if unknown.
func GetType(key string) (TypeDef, bool) {
	t, ok := typeIndex[key]
	return t, ok
}

// ListTypes returns all registered credential types.
func ListTypes() []TypeDef {
	out := make([]TypeDef, 0, len(typeIndex))
	for _, t := range Types {
		t.Source = "code"
		t.Editable = false
		out = append(out, t)
	}
	return out
}
