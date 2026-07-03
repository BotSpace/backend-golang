package ticket

import "time"

// TicketMessageResponse is the public projection of a single message.
type TicketMessageResponse struct {
	ID            string    `json:"id"`
	Sender        string    `json:"sender"`
	AuthorID      string    `json:"author_id,omitempty"`
	Message       string    `json:"message"`
	AttachmentURL string    `json:"attachment_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// TicketLastMessageResponse is the compact preview shown in a listing.
type TicketLastMessageResponse struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
}

// TicketResponse is the public projection of a ticket. Messages is nil in
// list results (LastMessage is populated instead) and populated in full for
// a single-ticket response.
type TicketResponse struct {
	ID          string                     `json:"id"`
	User        string                     `json:"user,omitempty"`
	UserPhone   string                     `json:"user_phone,omitempty"`
	Subject     string                     `json:"subject"`
	Status      string                     `json:"status"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
	LastMessage *TicketLastMessageResponse `json:"last_message"`
	Messages    []TicketMessageResponse    `json:"messages,omitempty"`
}

func toMessageResponse(m TicketMessage) TicketMessageResponse {
	authorID := ""
	if m.AuthorID != nil {
		authorID = m.AuthorID.String()
	}
	return TicketMessageResponse{
		ID:            m.ID.String(),
		Sender:        m.Sender,
		AuthorID:      authorID,
		Message:       m.Message,
		AttachmentURL: m.AttachmentURL,
		CreatedAt:     m.CreatedAt,
	}
}

func toResponse(t *Ticket) TicketResponse {
	resp := TicketResponse{
		ID:        t.ID.String(),
		User:      t.UserID.String(),
		UserPhone: t.UserPhone,
		Subject:   t.Subject,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
	if t.LastMessage != nil {
		resp.LastMessage = &TicketLastMessageResponse{Sender: t.LastMessage.Sender, Message: t.LastMessage.Message}
	}
	if t.Messages != nil {
		resp.Messages = make([]TicketMessageResponse, len(t.Messages))
		for i, m := range t.Messages {
			resp.Messages[i] = toMessageResponse(m)
		}
	}
	return resp
}

// ListResponse is a paginated envelope for a ticket listing.
type ListResponse struct {
	Items  []TicketResponse `json:"items"`
	Total  int64            `json:"total"`
	Limit  int32            `json:"limit"`
	Offset int32            `json:"offset"`
}

func toListResponse(items []Ticket, total int64, limit, offset int32) ListResponse {
	out := make([]TicketResponse, len(items))
	for i := range items {
		out[i] = toResponse(&items[i])
	}
	return ListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// CreateTicketRequest opens a new ticket with its first message.
type CreateTicketRequest struct {
	Subject       string `json:"subject" validate:"required,min=1,max=255"`
	Message       string `json:"message" validate:"required,min=1"`
	AttachmentURL string `json:"attachment_url" validate:"omitempty,max=2048"`
}

// ReplyRequest appends a message to an existing ticket.
type ReplyRequest struct {
	Message       string `json:"message" validate:"required,min=1"`
	AttachmentURL string `json:"attachment_url" validate:"omitempty,max=2048"`
}
