package kanban

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/wsevents"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc  Usecase
	sub wsevents.Subscriber
}

func NewHandler(uc Usecase) *Handler { return &Handler{uc: uc} }
func NewHandlerWithSubscriber(uc Usecase, sub wsevents.Subscriber) *Handler {
	return &Handler{uc: uc, sub: sub}
}

func ownerID(c *gin.Context) (uuid.UUID, bool) {
	id, e := uuid.Parse(middleware.UserID(c))
	if e != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}
func routeID(c *gin.Context, key string) (uuid.UUID, bool) {
	id, e := uuid.Parse(c.Param(key))
	if e != nil {
		httpx.Error(c, httpx.BadRequest("kanban.invalid_id", "invalid id"))
		return uuid.Nil, false
	}
	return id, true
}
func bind(c *gin.Context, v any) bool {
	if e := c.ShouldBindJSON(v); e != nil {
		httpx.Error(c, httpx.BadRequest("request.invalid", "invalid request body"))
		return false
	}
	return true
}
func queryUUID(c *gin.Context, key string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, true
	}
	id, e := uuid.Parse(raw)
	if e != nil {
		httpx.Error(c, httpx.BadRequest("kanban.invalid_project_id", "invalid project id"))
		return nil, false
	}
	return &id, true
}

// ListBoards godoc
// @Summary List Kanban boards
// @Tags kanban
// @Security BearerAuth
// @Param project_id query string false "Filter by linked bot/project"
// @Success 200 {array} Board
// @Router /boards [get]
func (h *Handler) ListBoards(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	p, ok := queryUUID(c, "project_id")
	if !ok {
		return
	}
	x, e := h.uc.ListBoards(c, o, p)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// ListArchivedBoards godoc
// @Summary List archived Kanban boards
// @Tags kanban
// @Security BearerAuth
// @Param project_id query string false "Filter by linked bot/project"
// @Success 200 {array} Board
// @Router /boards/archived [get]
func (h *Handler) ListArchivedBoards(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	p, ok := queryUUID(c, "project_id")
	if !ok {
		return
	}
	x, e := h.uc.ListArchivedBoards(c, o, p)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// Events godoc
// @Summary Stream owner Kanban changes as server-sent events
// @Tags kanban
// @Security BearerAuth
// @Produce text/event-stream
// @Success 200 {string} string
// @Router /boards/events [get]
func (h *Handler) Events(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	if h.sub == nil {
		httpx.Error(c, httpx.Internal(fmt.Errorf("kanban event stream is unavailable")))
		return
	}
	events, err := h.sub.Subscribe(c.Request.Context(), wsevents.UserRoom(o))
	if err != nil {
		httpx.Error(c, httpx.Internal(err))
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	// Flush the response headers immediately. Without this the first bytes only
	// arrive with an event (or the 20s keepalive), so browsers can remain in the
	// EventSource CONNECTING state and miss Pub/Sub events published meanwhile.
	c.Writer.Flush()
	c.Stream(func(w io.Writer) bool {
		select {
		case event, open := <-events:
			if !open {
				return false
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, event.Data)
			return true
		case <-c.Request.Context().Done():
			return false
		case <-time.After(20 * time.Second):
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			return true
		}
	})
}

// CreateBoard godoc
// @Summary Create a Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param body body CreateBoardRequest true "Board"
// @Success 201 {object} Board
// @Router /boards [post]
func (h *Handler) CreateBoard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	var r CreateBoardRequest
	if !bind(c, &r) {
		return
	}
	b, e := h.uc.CreateBoard(c, CreateBoardInput{OwnerID: o, Name: r.Name, Description: r.Description, Icon: r.Icon, Color: r.Color, CardTypeLabel: r.CardTypeLabel, FieldSchema: r.FieldSchema, ProjectIDs: r.ProjectIDs, Columns: r.Columns})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusCreated, b)
}

// GetBoard godoc
// @Summary Get a Kanban board with columns and cards
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param project_id query string false "Filter cards by source bot/project"
// @Success 200 {object} Board
// @Router /boards/{id} [get]
func (h *Handler) GetBoard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	p, ok := queryUUID(c, "project_id")
	if !ok {
		return
	}
	x, e := h.uc.GetBoard(c, id, o, p)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// UpdateBoard godoc
// @Summary Update a Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param body body UpdateBoardRequest true "Board"
// @Success 200 {object} Board
// @Router /boards/{id} [put]
func (h *Handler) UpdateBoard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r UpdateBoardRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.UpdateBoard(c, id, o, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// ArchiveBoard godoc
// @Summary Archive a Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Success 204
// @Router /boards/{id} [delete]
func (h *Handler) ArchiveBoard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	if e := h.uc.ArchiveBoard(c, id, o); e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.NoContent(c)
}

// RestoreBoard godoc
// @Summary Restore an archived Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Success 200 {object} Board
// @Router /boards/{id}/restore [post]
func (h *Handler) RestoreBoard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.RestoreBoard(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// LinkProject godoc
// @Summary Link a bot/project to a Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param body body ProjectLinkRequest true "Project"
// @Success 204
// @Router /boards/{id}/projects [post]
func (h *Handler) LinkProject(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r ProjectLinkRequest
	if !bind(c, &r) {
		return
	}
	if e := h.uc.LinkProject(c, id, r.ProjectID, o); e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.NoContent(c)
}

// UnlinkProject godoc
// @Summary Unlink a bot/project from a Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param project_id path string true "Project id"
// @Success 204
// @Router /boards/{id}/projects/{project_id} [delete]
func (h *Handler) UnlinkProject(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	p, ok := routeID(c, "project_id")
	if !ok {
		return
	}
	if e := h.uc.UnlinkProject(c, id, p, o); e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.NoContent(c)
}

// CreateColumn godoc
// @Summary Create a board column
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param body body ColumnInput true "Column"
// @Success 201 {object} Column
// @Router /boards/{id}/columns [post]
func (h *Handler) CreateColumn(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	b, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r ColumnInput
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.CreateColumn(c, b, o, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 201, x)
}

// UpdateColumn godoc
// @Summary Update a board column
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param column_id path string true "Column id"
// @Param body body ColumnInput true "Column"
// @Success 200 {object} Column
// @Router /boards/{id}/columns/{column_id} [put]
func (h *Handler) UpdateColumn(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "column_id")
	if !ok {
		return
	}
	var r ColumnInput
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.UpdateColumn(c, id, o, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// ReorderColumns godoc
// @Summary Reorder every column in a board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param body body ReorderColumnsRequest true "Complete ordered column id list"
// @Success 200 {array} Column
// @Router /boards/{id}/columns/reorder [post]
func (h *Handler) ReorderColumns(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	b, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r ReorderColumnsRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.ReorderColumns(c, b, o, r.ColumnIDs)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// DeleteColumn godoc
// @Summary Delete an empty board column
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param column_id path string true "Column id"
// @Success 204
// @Router /boards/{id}/columns/{column_id} [delete]
func (h *Handler) DeleteColumn(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "column_id")
	if !ok {
		return
	}
	if e := h.uc.DeleteColumn(c, id, o); e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.NoContent(c)
}

// CreateCard godoc
// @Summary Create a Kanban card
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param body body CreateCardRequest true "Card"
// @Success 201 {object} Card
// @Router /boards/{id}/cards [post]
func (h *Handler) CreateCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	b, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r CreateCardRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.CreateCard(c, CardInput{OwnerID: o, BoardID: b, ColumnID: r.ColumnID, SourceProjectID: r.SourceProjectID, Kind: r.Kind, Title: r.Title, Description: r.Description, Priority: r.Priority, DueAt: r.DueAt, ExternalKey: r.ExternalKey, CustomFields: r.CustomFields})
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 201, x)
}

// GetCard godoc
// @Summary Get a Kanban card
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Success 200 {object} Card
// @Router /cards/{id} [get]
func (h *Handler) GetCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.GetCard(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// UpdateCard godoc
// @Summary Update a Kanban card
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Param body body UpdateCardRequest true "Card"
// @Success 200 {object} Card
// @Router /cards/{id} [put]
func (h *Handler) UpdateCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r UpdateCardRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.UpdateCard(c, id, o, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// MoveCard godoc
// @Summary Move a Kanban card
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Param body body MoveCardRequest true "Destination"
// @Success 200 {object} Card
// @Router /cards/{id}/move [post]
func (h *Handler) MoveCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r MoveCardRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.MoveCard(c, id, o, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// ArchiveCard godoc
// @Summary Archive a Kanban card
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Success 200 {object} Card
// @Router /cards/{id} [delete]
func (h *Handler) ArchiveCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.ArchiveCard(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// ListArchivedCards godoc
// @Summary List archived cards for an active Kanban board
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Board id"
// @Param project_id query string false "Filter by source bot/project"
// @Success 200 {array} Card
// @Router /boards/{id}/cards/archived [get]
func (h *Handler) ListArchivedCards(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	p, ok := queryUUID(c, "project_id")
	if !ok {
		return
	}
	x, e := h.uc.ListArchivedCards(c, id, o, p)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// RestoreCard godoc
// @Summary Restore an archived Kanban card to its original column
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Success 200 {object} Card
// @Router /cards/{id}/restore [post]
func (h *Handler) RestoreCard(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.RestoreCard(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, http.StatusOK, x)
}

// ListActivities godoc
// @Summary List card activity
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Success 200 {array} Activity
// @Router /cards/{id}/activities [get]
func (h *Handler) ListActivities(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.ListActivities(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// ListComments godoc
// @Summary List card comments
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Success 200 {array} Comment
// @Router /cards/{id}/comments [get]
func (h *Handler) ListComments(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	x, e := h.uc.ListComments(c, id, o)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 200, x)
}

// CreateComment godoc
// @Summary Add a card comment
// @Tags kanban
// @Security BearerAuth
// @Param id path string true "Card id"
// @Param body body CommentRequest true "Comment"
// @Success 201 {object} Comment
// @Router /cards/{id}/comments [post]
func (h *Handler) CreateComment(c *gin.Context) {
	o, ok := ownerID(c)
	if !ok {
		return
	}
	id, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r CommentRequest
	if !bind(c, &r) {
		return
	}
	x, e := h.uc.CreateComment(c, id, o, r.Body)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	httpx.OK(c, 201, x)
}

// InternalUpsertCard godoc
// @Summary Upsert a bot-sourced Kanban card
// @Tags kanban-internal
// @Param X-Internal-Token header string true "Internal token"
// @Param project_id path string true "Source project id"
// @Param id path string true "Board id"
// @Param body body InternalUpsertCardRequest true "Card"
// @Success 200 {object} Card
// @Success 201 {object} Card
// @Router /internal/projects/{project_id}/boards/{id}/cards/upsert [post]
func (h *Handler) InternalUpsertCard(c *gin.Context) {
	p, ok := routeID(c, "project_id")
	if !ok {
		return
	}
	b, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r InternalUpsertCardRequest
	if !bind(c, &r) {
		return
	}
	x, created, e := h.uc.UpsertInternalCard(c, p, b, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	status := 200
	if created {
		status = 201
	}
	httpx.OK(c, status, x)
}

// InternalMutateCard godoc
// @Summary Create, upsert, update, move, or archive a bot-sourced Kanban card
// @Tags kanban-internal
// @Param X-Internal-Token header string true "Internal token"
// @Param project_id path string true "Source project id"
// @Param id path string true "Board id"
// @Param body body InternalUpsertCardRequest true "Mutation"
// @Success 200 {object} Card
// @Success 201 {object} Card
// @Router /internal/projects/{project_id}/boards/{id}/cards/mutate [post]
func (h *Handler) InternalMutateCard(c *gin.Context) {
	p, ok := routeID(c, "project_id")
	if !ok {
		return
	}
	b, ok := routeID(c, "id")
	if !ok {
		return
	}
	var r InternalUpsertCardRequest
	if !bind(c, &r) {
		return
	}
	x, created, e := h.uc.MutateInternalCard(c, p, b, r)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.OK(c, status, x)
}
