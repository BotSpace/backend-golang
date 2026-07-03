package chat

// ListUsersResponse is the wire shape for GET /api/v1/chat/users/{project_id}/.
//
// It carries the payload under BOTH the Django-native keys ("users"/"total"/
// "limit"/"skip", matching core/apps/api/views/chat.py's
// get_users_with_messages() response exactly) AND DRF-pagination-shaped
// aliases ("results"/"count"/"total_items"/"page_size"). The second set
// exists purely so agentv3's backend.py `_unwrap_list()` — which only
// recognises a raw JSON array or a `{"results": [...], "count": N}` dict,
// never Django's own "users"/"total" keys — can parse this endpoint; see
// list_chat_users in agent/src/agent/integrations/backend.py.
type ListUsersResponse struct {
	Results    []UserWithStats `json:"results"`
	Count      int64           `json:"count"`
	TotalItems int64           `json:"total_items"`
	PageSize   int64           `json:"page_size"`

	Users []UserWithStats `json:"users"`
	Total int64           `json:"total"`
	Limit int64           `json:"limit"`
	Skip  int64           `json:"skip"`
}

func toListUsersResponse(page UsersPage, limit, skip int64) ListUsersResponse {
	items := page.Items
	if items == nil {
		items = []UserWithStats{}
	}
	return ListUsersResponse{
		Results: items, Count: page.Total, TotalItems: page.Total, PageSize: limit,
		Users: items, Total: page.Total, Limit: limit, Skip: skip,
	}
}

// ListMessagesResponse is GET /api/v1/chat/messages/{project_id}/{chat_id}/'s
// wire shape — same dual-key rationale as ListUsersResponse (see its doc
// comment); mirrors get_user_messages()'s {"messages": [...], "total", ...}.
type ListMessagesResponse struct {
	Results    []map[string]any `json:"results"`
	Count      int64            `json:"count"`
	TotalItems int64            `json:"total_items"`
	PageSize   int64            `json:"page_size"`

	Messages []map[string]any `json:"messages"`
	Total    int64            `json:"total"`
	Limit    int64            `json:"limit"`
	Skip     int64            `json:"skip"`
}

func toListMessagesResponse(page MessagesPage, limit, skip int64) ListMessagesResponse {
	items := page.Items
	if items == nil {
		items = []map[string]any{}
	}
	return ListMessagesResponse{
		Results: items, Count: page.Total, TotalItems: page.Total, PageSize: limit,
		Messages: items, Total: page.Total, Limit: limit, Skip: skip,
	}
}
