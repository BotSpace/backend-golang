package kanban

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

type stubRepo struct {
	created     CreateBoardInput
	board       *Board
	card        *Card
	createdCard bool
}

func (s *stubRepo) CreateBoard(_ context.Context, in CreateBoardInput) (*Board, error) {
	s.created = in
	if s.board != nil {
		return s.board, nil
	}
	return &Board{ID: uuid.New(), OwnerID: in.OwnerID, Name: in.Name}, nil
}
func (s *stubRepo) ListBoards(context.Context, uuid.UUID, *uuid.UUID) ([]Board, error) {
	return []Board{}, nil
}
func (s *stubRepo) GetBoard(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID) (*Board, error) {
	return s.board, nil
}
func (s *stubRepo) UpdateBoard(_ context.Context, b Board) (*Board, error)               { return &b, nil }
func (s *stubRepo) ArchiveBoard(context.Context, uuid.UUID, uuid.UUID) error             { return nil }
func (s *stubRepo) LinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error   { return nil }
func (s *stubRepo) UnlinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (s *stubRepo) CreateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error) {
	return &Column{}, nil
}
func (s *stubRepo) UpdateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error) {
	return &Column{}, nil
}
func (s *stubRepo) DeleteColumn(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *stubRepo) CreateCard(_ context.Context, in CardInput) (*Card, error) {
	return &Card{ID: uuid.New(), BoardID: in.BoardID, ColumnID: in.ColumnID, Title: in.Title}, nil
}
func (s *stubRepo) GetCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)     { return s.card, nil }
func (s *stubRepo) UpdateCard(_ context.Context, _ uuid.UUID, c Card) (*Card, error) { return &c, nil }
func (s *stubRepo) MoveCard(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (*Card, error) {
	return s.card, nil
}
func (s *stubRepo) ArchiveCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error) {
	return s.card, nil
}
func (s *stubRepo) UpsertInternalCard(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ CardInput) (*Board, *Card, bool, error) {
	return s.board, s.card, s.createdCard, nil
}
func (s *stubRepo) ListActivities(context.Context, uuid.UUID, uuid.UUID) ([]Activity, error) {
	return []Activity{}, nil
}
func (s *stubRepo) CreateComment(context.Context, uuid.UUID, uuid.UUID, string) (*Comment, error) {
	return &Comment{}, nil
}
func (s *stubRepo) ListComments(context.Context, uuid.UUID, uuid.UUID) ([]Comment, error) {
	return []Comment{}, nil
}

type capturedPublisher struct{ event string }

func (p *capturedPublisher) Publish(_ context.Context, _, event string, _ any) { p.event = event }

func TestCreateBoardDefaultsColumnsAndPublishes(t *testing.T) {
	owner := uuid.New()
	repo := &stubRepo{}
	pub := &capturedPublisher{}
	uc := NewUsecase(repo, pub)
	b, err := uc.CreateBoard(t.Context(), CreateBoardInput{OwnerID: owner, Name: " Sales ", FieldSchema: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Sales" {
		t.Fatalf("name=%q", b.Name)
	}
	if len(repo.created.Columns) != 3 {
		t.Fatalf("columns=%d", len(repo.created.Columns))
	}
	if pub.event != "kanban_board_created" {
		t.Fatalf("event=%q", pub.event)
	}
}

func TestCreateBoardRejectsInvalidSchema(t *testing.T) {
	_, err := NewUsecase(&stubRepo{}, nil).CreateBoard(t.Context(), CreateBoardInput{OwnerID: uuid.New(), Name: "Board", FieldSchema: json.RawMessage(`{`)})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestInternalUpsertPublishesToBoardOwner(t *testing.T) {
	owner := uuid.New()
	card := &Card{ID: uuid.New(), Title: "Lead"}
	repo := &stubRepo{board: &Board{OwnerID: owner}, card: card, createdCard: true}
	pub := &capturedPublisher{}
	got, created, err := NewUsecase(repo, pub).UpsertInternalCard(t.Context(), uuid.New(), uuid.New(), InternalUpsertCardRequest{ColumnID: uuid.New(), Title: "Lead", CustomFields: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !created || got.ID != card.ID {
		t.Fatalf("created=%v card=%#v", created, got)
	}
	if pub.event != "kanban_card_created" {
		t.Fatalf("event=%q", pub.event)
	}
}
