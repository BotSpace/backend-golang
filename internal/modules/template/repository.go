package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. Submission approval and order payment
// touch several tables atomically, so the pool is kept alongside the queries
// to open ad-hoc transactions (same pattern as internal/modules/ticket).
type pgRepo struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, queries: sqlcdb.New(pool)}
}

func (r *pgRepo) UpdateLinkedProjectFlow(ctx context.Context, templateID uuid.UUID, flow json.RawMessage) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE projects AS p
		SET flow = $2, updated_at = NOW()
		FROM template AS t
		WHERE t.id = $1 AND p.id = t.project_id
	`, templateID, flow)
	if err != nil {
		return fmt.Errorf("update linked template project flow: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}
	return nil
}

// ===================== categories & features =====================

func (r *pgRepo) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := r.queries.TemplateListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list template categories: %w", err)
	}
	out := make([]Category, len(rows))
	for i, row := range rows {
		out[i] = categoryFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CreateCategory(ctx context.Context, name string) (*Category, error) {
	row, err := r.queries.TemplateCreateCategory(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("create template category: %w", err)
	}
	c := categoryFromSQLC(row)
	return &c, nil
}

func (r *pgRepo) ListFeatures(ctx context.Context) ([]Feature, error) {
	rows, err := r.queries.TemplateListFeatures(ctx)
	if err != nil {
		return nil, fmt.Errorf("list template features: %w", err)
	}
	out := make([]Feature, len(rows))
	for i, row := range rows {
		out[i] = featureFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CreateFeature(ctx context.Context, name, value string) (*Feature, error) {
	row, err := r.queries.TemplateCreateFeature(ctx, sqlcdb.TemplateCreateFeatureParams{
		Name:  name,
		Value: strPtr(value),
	})
	if err != nil {
		return nil, fmt.Errorf("create template feature: %w", err)
	}
	f := featureFromSQLC(row)
	return &f, nil
}

// ===================== catalog =====================

func (r *pgRepo) GetBySlug(ctx context.Context, slug string) (*Template, error) {
	row, err := r.queries.TemplateGetBySlug(ctx, slug)
	if err != nil {
		return nil, mapNoRows(err)
	}
	t := templateFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Template, error) {
	row, err := r.queries.TemplateGetByID(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	t := templateFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) GetBySlugAny(ctx context.Context, slug string) (*Template, error) {
	row, err := r.queries.TemplateGetBySlugAny(ctx, slug)
	if err != nil {
		return nil, mapNoRows(err)
	}
	t := templateFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) GetProjectFlow(ctx context.Context, projectID uuid.UUID) (json.RawMessage, error) {
	raw, err := r.queries.TemplateGetProjectFlow(ctx, projectID)
	if err != nil {
		return nil, mapNoRows(err)
	}
	return json.RawMessage(raw), nil
}

func (r *pgRepo) IncrementViews(ctx context.Context, id uuid.UUID) error {
	if err := r.queries.TemplateIncrementViews(ctx, id); err != nil {
		return fmt.Errorf("increment template views: %w", err)
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, f TemplateListFilter) ([]Template, error) {
	rows, err := r.queries.TemplateListPublic(ctx, sqlcdb.TemplateListPublicParams{
		CategoryID: f.CategoryID,
		FeatureID:  f.FeatureID,
		MinPrice:   optionalNumeric(f.MinPrice),
		MaxPrice:   optionalNumeric(f.MaxPrice),
		Keywords:   searchKeywords(f.Search),
		Limit:      f.Limit,
		Offset:     f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	out := make([]Template, len(rows))
	for i, row := range rows {
		out[i] = templateFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, f TemplateListFilter) (int64, error) {
	n, err := r.queries.TemplateCountPublic(ctx, sqlcdb.TemplateCountPublicParams{
		CategoryID: f.CategoryID,
		FeatureID:  f.FeatureID,
		MinPrice:   optionalNumeric(f.MinPrice),
		MaxPrice:   optionalNumeric(f.MaxPrice),
		Keywords:   searchKeywords(f.Search),
	})
	if err != nil {
		return 0, fmt.Errorf("count templates: %w", err)
	}
	return n, nil
}

// AttachExtras batch-loads categories, features and like counts for the given
// templates (mutating items in place), plus per-viewer liked/ordered flags
// when viewerID is set. One round trip per aspect regardless of list size.
func (r *pgRepo) AttachExtras(ctx context.Context, items []Template, viewerID *uuid.UUID) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	index := make(map[uuid.UUID]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		index[items[i].ID] = i
	}

	catRows, err := r.queries.TemplateCategoriesForTemplates(ctx, ids)
	if err != nil {
		return fmt.Errorf("load template categories: %w", err)
	}
	for _, row := range catRows {
		if i, ok := index[row.TemplateID]; ok {
			items[i].Categories = append(items[i].Categories, Category{ID: row.ID, Name: row.Name})
		}
	}

	featRows, err := r.queries.TemplateFeaturesForTemplates(ctx, ids)
	if err != nil {
		return fmt.Errorf("load template features: %w", err)
	}
	for _, row := range featRows {
		if i, ok := index[row.TemplateID]; ok {
			items[i].Features = append(items[i].Features, Feature{ID: row.ID, Name: row.Name, Value: deref(row.Value)})
		}
	}

	likeRows, err := r.queries.TemplateLikeCounts(ctx, ids)
	if err != nil {
		return fmt.Errorf("load template like counts: %w", err)
	}
	for _, row := range likeRows {
		if i, ok := index[row.TemplateID]; ok {
			items[i].LikesCount = row.Cnt
		}
	}

	if viewerID == nil {
		return nil
	}

	likedIDs, err := r.queries.TemplateLikedIDsForUser(ctx, sqlcdb.TemplateLikedIDsForUserParams{
		UserID:      viewerID,
		TemplateIds: ids,
	})
	if err != nil {
		return fmt.Errorf("load liked templates: %w", err)
	}
	for _, id := range likedIDs {
		if i, ok := index[id]; ok {
			items[i].IsLiked = true
		}
	}

	orderedIDs, err := r.queries.TemplateOrderedIDsForUser(ctx, sqlcdb.TemplateOrderedIDsForUserParams{
		UserID:      viewerID,
		TemplateIds: ids,
	})
	if err != nil {
		return fmt.Errorf("load ordered templates: %w", err)
	}
	for _, id := range orderedIDs {
		if id == nil {
			continue
		}
		if i, ok := index[*id]; ok {
			items[i].IsOrdered = true
		}
	}
	return nil
}

// ===================== catalog (admin) =====================

func (r *pgRepo) ListAdmin(ctx context.Context, f AdminTemplateListFilter) ([]AdminTemplateSummary, error) {
	rows, err := r.queries.TemplateListAdmin(ctx, sqlcdb.TemplateListAdminParams{
		IsVerify: f.IsVerify,
		Search:   strPtr(f.Search),
		Limit:    f.Limit,
		Offset:   f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list templates admin: %w", err)
	}
	out := make([]AdminTemplateSummary, len(rows))
	for i, row := range rows {
		out[i] = AdminTemplateSummary{
			Template:    templateFromSQLC(row.Template),
			AuthorName:  displayName(row.AuthorFirstName, row.AuthorLastName, row.AuthorPhone, row.AuthorUsername),
			AuthorPhone: deref(row.AuthorPhone),
		}
	}
	if err := r.attachExtrasToSummaries(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pgRepo) CountAdmin(ctx context.Context, f AdminTemplateListFilter) (int64, error) {
	n, err := r.queries.TemplateCountAdmin(ctx, sqlcdb.TemplateCountAdminParams{
		IsVerify: f.IsVerify,
		Search:   strPtr(f.Search),
	})
	if err != nil {
		return 0, fmt.Errorf("count templates admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) GetAdminByID(ctx context.Context, id uuid.UUID) (*AdminTemplateSummary, error) {
	row, err := r.queries.TemplateGetAdmin(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	items := []AdminTemplateSummary{{
		Template:    templateFromSQLC(row.Template),
		AuthorName:  displayName(row.AuthorFirstName, row.AuthorLastName, row.AuthorPhone, row.AuthorUsername),
		AuthorPhone: deref(row.AuthorPhone),
	}}
	if err := r.attachExtrasToSummaries(ctx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

// attachExtrasToSummaries batch-loads categories/features/likes onto the
// embedded Template of each summary, in place.
func (r *pgRepo) attachExtrasToSummaries(ctx context.Context, items []AdminTemplateSummary) error {
	if len(items) == 0 {
		return nil
	}
	tmpls := make([]Template, len(items))
	for i := range items {
		tmpls[i] = items[i].Template
	}
	if err := r.AttachExtras(ctx, tmpls, nil); err != nil {
		return err
	}
	for i := range items {
		items[i].Template = tmpls[i]
	}
	return nil
}

func (r *pgRepo) CreateAdmin(ctx context.Context, in CreateAdminTemplateInput) (*Template, error) {
	priceNum, err := stringToNumeric(in.OriginalPrice)
	if err != nil {
		return nil, err
	}
	discountNum, err := stringToNumeric(in.Discount)
	if err != nil {
		return nil, err
	}
	slug := in.Slug
	if slug == "" {
		s, err := uniqueSlug(ctx, r.queries, in.Name)
		if err != nil {
			return nil, err
		}
		slug = s
	}
	row, err := r.queries.TemplateAdminCreate(ctx, sqlcdb.TemplateAdminCreateParams{
		AuthorID:         in.AuthorID,
		ProjectID:        in.ProjectID,
		Name:             in.Name,
		Slug:             slug,
		Image:            in.Image,
		Video:            strPtr(in.Video),
		ShortDescription: in.ShortDescription,
		Description:      in.Description,
		OriginalPrice:    priceNum,
		Discount:         discountNum,
		DiscountType:     discountTypeOrDefault(in.DiscountType),
		DemoUrl:          strPtr(in.DemoURL),
		IsVerify:         in.IsVerify,
		RequiredEnvs:     rawOrEmptyArray(in.RequiredEnvs),
	})
	if err != nil {
		return nil, fmt.Errorf("admin create template: %w", err)
	}
	if err := r.setTemplateTags(ctx, row.ID, in.CategoryIDs, in.FeatureIDs); err != nil {
		return nil, err
	}
	t := templateFromSQLC(row)
	if err := r.AttachExtras(ctx, []Template{t}, nil); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *pgRepo) UpdateAdmin(ctx context.Context, id uuid.UUID, in UpdateAdminTemplateInput) (*Template, error) {
	var price, discount pgtype.Numeric
	if in.OriginalPrice != nil {
		p, err := stringToNumeric(*in.OriginalPrice)
		if err != nil {
			return nil, err
		}
		price = p
	}
	if in.Discount != nil {
		d, err := stringToNumeric(*in.Discount)
		if err != nil {
			return nil, err
		}
		discount = d
	}
	row, err := r.queries.TemplateAdminUpdate(ctx, sqlcdb.TemplateAdminUpdateParams{
		ID:               id,
		Name:             in.Name,
		Image:            in.Image,
		Video:            in.Video,
		ShortDescription: in.ShortDescription,
		Description:      in.Description,
		OriginalPrice:    price,
		Discount:         discount,
		DiscountType:     in.DiscountType,
		DemoUrl:          in.DemoURL,
		IsVerify:         in.IsVerify,
		RequiredEnvs:     rawOrNil(in.RequiredEnvs),
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	if in.CategoryIDs != nil || in.FeatureIDs != nil {
		if err := r.setTemplateTags(ctx, id, in.CategoryIDs, in.FeatureIDs); err != nil {
			return nil, err
		}
	}
	t := templateFromSQLC(row)
	if err := r.AttachExtras(ctx, []Template{t}, nil); err != nil {
		return nil, err
	}
	return &t, nil
}

// setTemplateTags replaces a template's category/feature tags when the
// corresponding slice is non-nil (nil means "leave as-is" for updates; for a
// fresh create both are always applied, even if empty, which is a no-op).
func (r *pgRepo) setTemplateTags(ctx context.Context, templateID uuid.UUID, categoryIDs, featureIDs []uuid.UUID) error {
	if categoryIDs != nil {
		if err := r.queries.TemplateClearCategories(ctx, templateID); err != nil {
			return fmt.Errorf("clear template categories: %w", err)
		}
		if len(categoryIDs) > 0 {
			if err := r.queries.TemplateSetCategories(ctx, sqlcdb.TemplateSetCategoriesParams{
				TemplateID: templateID, CategoryIds: categoryIDs,
			}); err != nil {
				return fmt.Errorf("set template categories: %w", err)
			}
		}
	}
	if featureIDs != nil {
		if err := r.queries.TemplateClearFeatures(ctx, templateID); err != nil {
			return fmt.Errorf("clear template features: %w", err)
		}
		if len(featureIDs) > 0 {
			if err := r.queries.TemplateSetFeatures(ctx, sqlcdb.TemplateSetFeaturesParams{
				TemplateID: templateID, FeatureIds: featureIDs,
			}); err != nil {
				return fmt.Errorf("set template features: %w", err)
			}
		}
	}
	return nil
}

func (r *pgRepo) DeleteAdmin(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries.TemplateAdminDelete(ctx, id)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) SetVerify(ctx context.Context, id uuid.UUID, verify bool) (*Template, error) {
	row, err := r.queries.TemplateSetVerify(ctx, sqlcdb.TemplateSetVerifyParams{ID: id, IsVerify: verify})
	if err != nil {
		return nil, mapNoRows(err)
	}
	t := templateFromSQLC(row)
	return &t, nil
}

// displayName mirrors User.__str__ fallback: "First Last" -> phone -> username
// (same convention as internal/modules/blog's helper of the same name).
func displayName(first, last, phone, username *string) string {
	f, l := deref(first), deref(last)
	full := strings.TrimSpace(f + " " + l)
	if full != "" {
		return full
	}
	if p := deref(phone); p != "" {
		return p
	}
	return deref(username)
}

// ===================== likes =====================

// ToggleLike deletes the like if one exists, otherwise inserts it - a plain
// (non-transactional) toggle is fine for a like button.
func (r *pgRepo) ToggleLike(ctx context.Context, templateID, userID uuid.UUID) (bool, error) {
	deleted, err := r.queries.TemplateDeleteLike(ctx, sqlcdb.TemplateDeleteLikeParams{
		TemplateID: templateID,
		UserID:     &userID,
	})
	if err != nil {
		return false, fmt.Errorf("delete template like: %w", err)
	}
	if deleted > 0 {
		return false, nil
	}
	if _, err := r.queries.TemplateInsertLike(ctx, sqlcdb.TemplateInsertLikeParams{
		TemplateID: templateID,
		UserID:     &userID,
	}); err != nil {
		return false, fmt.Errorf("insert template like: %w", err)
	}
	return true, nil
}

// ===================== submissions =====================

func (r *pgRepo) ProjectOwnedByUser(ctx context.Context, projectID, ownerID uuid.UUID) (bool, error) {
	ok, err := r.queries.TemplateProjectOwnedByUser(ctx, sqlcdb.TemplateProjectOwnedByUserParams{
		ProjectID: projectID, OwnerID: ownerID,
	})
	if err != nil {
		return false, fmt.Errorf("check project ownership: %w", err)
	}
	return ok, nil
}

func (r *pgRepo) CreateSubmission(ctx context.Context, in CreateSubmissionInput) (*Submission, error) {
	price, err := stringToNumeric(in.OriginalPrice)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.TemplateCreateSubmission(ctx, sqlcdb.TemplateCreateSubmissionParams{
		UserID:           in.UserID,
		ProjectID:        in.ProjectID,
		Name:             in.Name,
		ShortDescription: in.ShortDescription,
		Description:      in.Description,
		Image:            strPtr(in.Image),
		Video:            strPtr(in.Video),
		DemoUrl:          strPtr(in.DemoURL),
		OriginalPrice:    price,
		RequiredEnvs:     rawOrEmptyArray(in.RequiredEnvs),
	})
	if err != nil {
		return nil, fmt.Errorf("create template submission: %w", err)
	}
	if len(in.CategoryIDs) > 0 {
		if err := r.queries.TemplateSetSubmissionCategories(ctx, sqlcdb.TemplateSetSubmissionCategoriesParams{
			SubmissionID: row.ID,
			CategoryIds:  in.CategoryIDs,
		}); err != nil {
			return nil, fmt.Errorf("set submission categories: %w", err)
		}
	}
	if len(in.FeatureIDs) > 0 {
		if err := r.queries.TemplateSetSubmissionFeatures(ctx, sqlcdb.TemplateSetSubmissionFeaturesParams{
			SubmissionID: row.ID,
			FeatureIds:   in.FeatureIDs,
		}); err != nil {
			return nil, fmt.Errorf("set submission features: %w", err)
		}
	}
	s := submissionFromSQLC(row)
	s.Categories = nil
	s.Features = nil
	if err := r.AttachSubmissionExtras(ctx, []Submission{s}); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *pgRepo) GetSubmissionForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Submission, error) {
	row, err := r.queries.TemplateGetSubmissionForOwner(ctx, sqlcdb.TemplateGetSubmissionForOwnerParams{
		ID: id, UserID: ownerID,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	s := submissionFromSQLC(row)
	if err := r.attachSubmissionExtrasPtr(ctx, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *pgRepo) GetSubmission(ctx context.Context, id uuid.UUID) (*Submission, error) {
	row, err := r.queries.TemplateGetSubmission(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	s := submissionFromSQLC(row)
	if err := r.attachSubmissionExtrasPtr(ctx, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *pgRepo) ListSubmissionsForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Submission, error) {
	rows, err := r.queries.TemplateListSubmissionsForOwner(ctx, sqlcdb.TemplateListSubmissionsForOwnerParams{
		UserID: ownerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	return r.submissionsFromRows(ctx, rows)
}

func (r *pgRepo) CountSubmissionsForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	n, err := r.queries.TemplateCountSubmissionsForOwner(ctx, ownerID)
	if err != nil {
		return 0, fmt.Errorf("count submissions: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListSubmissionsAdmin(ctx context.Context, status string, limit, offset int32) ([]Submission, error) {
	rows, err := r.queries.TemplateListSubmissionsAdmin(ctx, sqlcdb.TemplateListSubmissionsAdminParams{
		Status: strPtr(status), Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list submissions admin: %w", err)
	}
	return r.submissionsFromRows(ctx, rows)
}

func (r *pgRepo) CountSubmissionsAdmin(ctx context.Context, status string) (int64, error) {
	n, err := r.queries.TemplateCountSubmissionsAdmin(ctx, strPtr(status))
	if err != nil {
		return 0, fmt.Errorf("count submissions admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) UpdateSubmission(ctx context.Context, id, ownerID uuid.UUID, in UpdateSubmissionInput) (*Submission, error) {
	var price pgtype.Numeric
	if in.OriginalPrice != nil {
		p, err := stringToNumeric(*in.OriginalPrice)
		if err != nil {
			return nil, err
		}
		price = p
	}
	row, err := r.queries.TemplateUpdateSubmission(ctx, sqlcdb.TemplateUpdateSubmissionParams{
		ID:               id,
		UserID:           ownerID,
		Name:             in.Name,
		ShortDescription: in.ShortDescription,
		Description:      in.Description,
		Image:            in.Image,
		Video:            in.Video,
		DemoUrl:          in.DemoURL,
		OriginalPrice:    price,
		RequiredEnvs:     rawOrNil(in.RequiredEnvs),
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	if in.CategoryIDs != nil {
		if err := r.queries.TemplateClearSubmissionCategories(ctx, id); err != nil {
			return nil, fmt.Errorf("clear submission categories: %w", err)
		}
		if len(in.CategoryIDs) > 0 {
			if err := r.queries.TemplateSetSubmissionCategories(ctx, sqlcdb.TemplateSetSubmissionCategoriesParams{
				SubmissionID: id, CategoryIds: in.CategoryIDs,
			}); err != nil {
				return nil, fmt.Errorf("set submission categories: %w", err)
			}
		}
	}
	if in.FeatureIDs != nil {
		if err := r.queries.TemplateClearSubmissionFeatures(ctx, id); err != nil {
			return nil, fmt.Errorf("clear submission features: %w", err)
		}
		if len(in.FeatureIDs) > 0 {
			if err := r.queries.TemplateSetSubmissionFeatures(ctx, sqlcdb.TemplateSetSubmissionFeaturesParams{
				SubmissionID: id, FeatureIds: in.FeatureIDs,
			}); err != nil {
				return nil, fmt.Errorf("set submission features: %w", err)
			}
		}
	}
	s := submissionFromSQLC(row)
	if err := r.attachSubmissionExtrasPtr(ctx, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *pgRepo) DeleteSubmission(ctx context.Context, id, ownerID uuid.UUID) error {
	rows, err := r.queries.TemplateDeleteSubmission(ctx, sqlcdb.TemplateDeleteSubmissionParams{ID: id, UserID: ownerID})
	if err != nil {
		return fmt.Errorf("delete submission: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

// AttachSubmissionExtras batch-loads categories/features for the given
// submissions (mutating items in place).
func (r *pgRepo) AttachSubmissionExtras(ctx context.Context, items []Submission) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	index := make(map[uuid.UUID]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		index[items[i].ID] = i
	}
	catRows, err := r.queries.TemplateSubmissionCategoriesFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("load submission categories: %w", err)
	}
	for _, row := range catRows {
		if i, ok := index[row.SubmissionID]; ok {
			items[i].Categories = append(items[i].Categories, Category{ID: row.ID, Name: row.Name})
		}
	}
	featRows, err := r.queries.TemplateSubmissionFeaturesFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("load submission features: %w", err)
	}
	for _, row := range featRows {
		if i, ok := index[row.SubmissionID]; ok {
			items[i].Features = append(items[i].Features, Feature{ID: row.ID, Name: row.Name, Value: deref(row.Value)})
		}
	}
	return nil
}

func (r *pgRepo) attachSubmissionExtrasPtr(ctx context.Context, s *Submission) error {
	items := []Submission{*s}
	if err := r.AttachSubmissionExtras(ctx, items); err != nil {
		return err
	}
	*s = items[0]
	return nil
}

func (r *pgRepo) submissionsFromRows(ctx context.Context, rows []sqlcdb.TemplateSubmissionRequest) ([]Submission, error) {
	out := make([]Submission, len(rows))
	for i, row := range rows {
		out[i] = submissionFromSQLC(row)
	}
	if err := r.AttachSubmissionExtras(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApproveSubmission creates the published Template row, copies the
// submission's categories/features onto it, and marks the submission
// approved - all inside one transaction so a mid-way failure never leaves a
// half-published template.
func (r *pgRepo) ApproveSubmission(
	ctx context.Context, id, reviewerID uuid.UUID, discount, discountType string,
) (*Submission, *Template, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin approve submission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	sub, err := q.TemplateGetSubmission(ctx, id)
	if err != nil {
		return nil, nil, mapNoRows(err)
	}
	if sub.Status != SubmissionStatusPending {
		return nil, nil, ErrSubmissionNotPending
	}

	discountNum, err := stringToNumeric(discount)
	if err != nil {
		return nil, nil, err
	}

	slug, err := uniqueSlug(ctx, q, sub.Name)
	if err != nil {
		return nil, nil, err
	}

	tmplRow, err := q.TemplateCreateFromSubmission(ctx, sqlcdb.TemplateCreateFromSubmissionParams{
		AuthorID:         &sub.UserID,
		ProjectID:        &sub.ProjectID,
		Name:             sub.Name,
		Image:            deref(sub.Image),
		Video:            sub.Video,
		ShortDescription: sub.ShortDescription,
		Description:      sub.Description,
		OriginalPrice:    sub.OriginalPrice,
		Discount:         discountNum,
		DiscountType:     discountTypeOrDefault(discountType),
		DemoUrl:          sub.DemoUrl,
		RequiredEnvs:     sub.RequiredEnvs,
		Slug:             slug,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create template from submission: %w", err)
	}

	if err := q.TemplateCopySubmissionCategories(ctx, sqlcdb.TemplateCopySubmissionCategoriesParams{
		TemplateID: tmplRow.ID, SubmissionID: id,
	}); err != nil {
		return nil, nil, fmt.Errorf("copy submission categories: %w", err)
	}
	if err := q.TemplateCopySubmissionFeatures(ctx, sqlcdb.TemplateCopySubmissionFeaturesParams{
		TemplateID: tmplRow.ID, SubmissionID: id,
	}); err != nil {
		return nil, nil, fmt.Errorf("copy submission features: %w", err)
	}

	approvedRow, err := q.TemplateApproveSubmission(ctx, sqlcdb.TemplateApproveSubmissionParams{
		ID: id, TemplateID: &tmplRow.ID, ReviewedBy: &reviewerID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mark submission approved: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit approve submission: %w", err)
	}

	s := submissionFromSQLC(approvedRow)
	if err := r.AttachSubmissionExtras(ctx, []Submission{s}); err != nil {
		return nil, nil, err
	}
	t := templateFromSQLC(tmplRow)
	if err := r.AttachExtras(ctx, []Template{t}, nil); err != nil {
		return nil, nil, err
	}
	return &s, &t, nil
}

func (r *pgRepo) RejectSubmission(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Submission, error) {
	row, err := r.queries.TemplateRejectSubmission(ctx, sqlcdb.TemplateRejectSubmissionParams{
		ID: id, RejectionReason: &reason, ReviewedBy: &reviewerID,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	s := submissionFromSQLC(row)
	if err := r.attachSubmissionExtrasPtr(ctx, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// uniqueSlug slugifies name and appends a short suffix on collision, retrying
// a handful of times (mirrors Django's implicit slugify-on-save, but Django
// never actually de-duplicates - this port makes the unique constraint
// always satisfiable instead of erroring on a repeat template name).
func uniqueSlug(ctx context.Context, q *sqlcdb.Queries, name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = "template"
	}
	candidate := base
	for i := 0; i < 20; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s-%s", base, uuid.New().String()[:8])
		}
		exists, err := q.TemplateSlugExists(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("check slug: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return fmt.Sprintf("%s-%s", base, uuid.New().String()), nil
}

// ===================== orders / purchase =====================

func (r *pgRepo) GetOrCreateOrder(ctx context.Context, userID, templateID uuid.UUID) (*Order, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	existing, err := q.TemplateGetOrderForUserAndTemplate(ctx, sqlcdb.TemplateGetOrderForUserAndTemplateParams{
		UserID: &userID, TemplateID: &templateID,
	})
	if err == nil {
		o := orderFromSQLC(existing)
		return &o, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup existing order: %w", err)
	}

	tmpl, err := q.TemplateGetByID(ctx, templateID)
	if err != nil {
		return nil, mapNoRows(err)
	}
	t := templateFromSQLC(tmpl)
	price := t.Price()

	status := OrderStatusPending
	paid := false
	commissionDone := false
	priceRat, _ := parseRat(price)
	free := priceRat == nil || priceRat.Sign() == 0
	if free {
		status = OrderStatusConfirm
		paid = true
		commissionDone = true
	}

	priceNum, err := stringToNumeric(price)
	if err != nil {
		return nil, err
	}
	row, err := q.TemplateCreateOrder(ctx, sqlcdb.TemplateCreateOrderParams{
		UserID:         &userID,
		TemplateID:     &templateID,
		Amount:         priceNum,
		Status:         status,
		PaymentStatus:  paid,
		CommissionPaid: commissionDone,
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	if free {
		if err := q.TemplateGrantOwnership(ctx, sqlcdb.TemplateGrantOwnershipParams{
			UserID: userID, TemplateID: templateID,
		}); err != nil {
			return nil, fmt.Errorf("grant free template ownership: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create order: %w", err)
	}
	o := orderFromSQLC(row)
	return &o, nil
}

func (r *pgRepo) GetOrderForUserAndTemplate(ctx context.Context, userID, templateID uuid.UUID) (*Order, error) {
	row, err := r.queries.TemplateGetOrderForUserAndTemplate(ctx, sqlcdb.TemplateGetOrderForUserAndTemplateParams{
		UserID: &userID, TemplateID: &templateID,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	o := orderFromSQLC(row)
	return &o, nil
}

func (r *pgRepo) GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error) {
	row, err := r.queries.TemplateGetOrderForOwner(ctx, sqlcdb.TemplateGetOrderForOwnerParams{ID: id, UserID: &ownerID})
	if err != nil {
		return nil, mapNoRows(err)
	}
	o := orderFromSQLC(row)
	return &o, nil
}

func (r *pgRepo) ListOrdersForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Order, error) {
	rows, err := r.queries.TemplateListOrdersForOwner(ctx, sqlcdb.TemplateListOrdersForOwnerParams{
		UserID: &ownerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	out := make([]Order, len(rows))
	for i, row := range rows {
		out[i] = orderFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountOrdersForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	n, err := r.queries.TemplateCountOrdersForOwner(ctx, &ownerID)
	if err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}
	return n, nil
}

// Pay charges the buyer's integer wallet balance, credits the author's
// commission (AuthorCommissionPercent), marks the order paid, and grants
// catalog ownership - all inside one transaction (mirrors the combination of
// perform_create's free-order auto-confirm and signals/order.py::OrderSignal
// for the paid path).
func (r *pgRepo) Pay(ctx context.Context, id, buyerID uuid.UUID) (*Order, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin pay order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	order, err := q.TemplateGetOrderForOwner(ctx, sqlcdb.TemplateGetOrderForOwnerParams{ID: id, UserID: &buyerID})
	if err != nil {
		return nil, mapNoRows(err)
	}
	if order.CommissionPaid {
		// Already paid - idempotent no-op.
		o := orderFromSQLC(order)
		return &o, nil
	}
	if order.TemplateID == nil {
		return nil, ErrTemplateUnavailable
	}
	tmplRow, err := q.TemplateGetByID(ctx, *order.TemplateID)
	if err != nil {
		return nil, mapNoRows(err)
	}

	amountStr := numericToString(order.Amount)
	amountInt, err := decimalToInt64(amountStr)
	if err != nil {
		return nil, err
	}
	if _, err := q.TemplateDeductBuyerBalance(ctx, sqlcdb.TemplateDeductBuyerBalanceParams{
		UserID: buyerID, Amount: amountInt,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInsufficientBalance
		}
		return nil, fmt.Errorf("deduct buyer balance: %w", err)
	}

	if tmplRow.AuthorID != nil {
		commission, err := commissionShare(amountStr)
		if err != nil {
			return nil, err
		}
		commissionNum, err := stringToNumeric(commission)
		if err != nil {
			return nil, err
		}
		if err := q.TemplateCreditAuthorCommission(ctx, sqlcdb.TemplateCreditAuthorCommissionParams{
			AuthorID: *tmplRow.AuthorID, Commission: commissionNum,
		}); err != nil {
			return nil, fmt.Errorf("credit author commission: %w", err)
		}
	}

	paidRow, err := q.TemplateMarkOrderPaid(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("mark order paid: %w", err)
	}
	if err := q.TemplateGrantOwnership(ctx, sqlcdb.TemplateGrantOwnershipParams{
		UserID: buyerID, TemplateID: *order.TemplateID,
	}); err != nil {
		return nil, fmt.Errorf("grant template ownership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit pay order: %w", err)
	}
	o := orderFromSQLC(paidRow)
	return &o, nil
}

// ===================== withdrawals =====================

func (r *pgRepo) GetAuthorBalance(ctx context.Context, userID uuid.UUID) (string, error) {
	n, err := r.queries.TemplateGetAuthorBalance(ctx, userID)
	if err != nil {
		return "", mapNoRows(err)
	}
	return numericToString(n), nil
}

func (r *pgRepo) CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount, cardNumber, cardOwner string) (*Withdrawal, error) {
	amountNum, err := stringToNumeric(amount)
	if err != nil {
		return nil, err
	}
	row, err := r.queries.TemplateCreateWithdrawal(ctx, sqlcdb.TemplateCreateWithdrawalParams{
		UserID: userID, Amount: amountNum, CardNumber: cardNumber, CardOwner: cardOwner,
	})
	if err != nil {
		return nil, fmt.Errorf("create withdrawal: %w", err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) GetWithdrawalForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Withdrawal, error) {
	row, err := r.queries.TemplateGetWithdrawalForOwner(ctx, sqlcdb.TemplateGetWithdrawalForOwnerParams{
		ID: id, UserID: ownerID,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) GetWithdrawal(ctx context.Context, id uuid.UUID) (*Withdrawal, error) {
	row, err := r.queries.TemplateGetWithdrawal(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) ListWithdrawalsForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Withdrawal, error) {
	rows, err := r.queries.TemplateListWithdrawalsForOwner(ctx, sqlcdb.TemplateListWithdrawalsForOwnerParams{
		UserID: ownerID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}
	out := make([]Withdrawal, len(rows))
	for i, row := range rows {
		out[i] = withdrawalFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountWithdrawalsForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	n, err := r.queries.TemplateCountWithdrawalsForOwner(ctx, ownerID)
	if err != nil {
		return 0, fmt.Errorf("count withdrawals: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]Withdrawal, error) {
	rows, err := r.queries.TemplateListWithdrawalsAdmin(ctx, sqlcdb.TemplateListWithdrawalsAdminParams{
		Status: strPtr(status), Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list withdrawals admin: %w", err)
	}
	out := make([]Withdrawal, len(rows))
	for i, row := range rows {
		w := withdrawalFromSQLC(row.TemplateWithdrawalRequest)
		w.UserPhone = deref(row.UserPhone)
		out[i] = w
	}
	return out, nil
}

func (r *pgRepo) CountWithdrawalsAdmin(ctx context.Context, status string) (int64, error) {
	n, err := r.queries.TemplateCountWithdrawalsAdmin(ctx, strPtr(status))
	if err != nil {
		return 0, fmt.Errorf("count withdrawals admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) DeleteWithdrawal(ctx context.Context, id, ownerID uuid.UUID) error {
	rows, err := r.queries.TemplateDeleteWithdrawal(ctx, sqlcdb.TemplateDeleteWithdrawalParams{ID: id, UserID: ownerID})
	if err != nil {
		return fmt.Errorf("delete withdrawal: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

// ApproveWithdrawal deducts users.template_balance inside a transaction; if
// the guarded deduction affects no row (balance dropped below the requested
// amount since the request was filed) it rejects instead, exactly mirroring
// admin/withdrawal.py::approve_withdrawals.
func (r *pgRepo) ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*Withdrawal, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin approve withdrawal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.queries.WithTx(tx)

	wd, err := q.TemplateGetWithdrawal(ctx, id)
	if err != nil {
		return nil, mapNoRows(err)
	}
	if wd.Status != WithdrawalStatusPending {
		return nil, ErrWithdrawalNotPending
	}

	_, err = q.TemplateApproveWithdrawalDeductBalance(ctx, sqlcdb.TemplateApproveWithdrawalDeductBalanceParams{
		UserID: wd.UserID, Amount: wd.Amount,
	})
	var row sqlcdb.TemplateWithdrawalRequest
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("deduct template balance: %w", err)
		}
		row, err = q.TemplateMarkWithdrawalRejected(ctx, sqlcdb.TemplateMarkWithdrawalRejectedParams{
			ID: id, RejectionReason: strPtr("Insufficient template balance at approval time"), ReviewedBy: &reviewerID,
		})
		if err != nil {
			return nil, fmt.Errorf("reject withdrawal: %w", err)
		}
	} else {
		row, err = q.TemplateMarkWithdrawalApproved(ctx, sqlcdb.TemplateMarkWithdrawalApprovedParams{
			ID: id, ReviewedBy: &reviewerID,
		})
		if err != nil {
			return nil, fmt.Errorf("approve withdrawal: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit approve withdrawal: %w", err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

func (r *pgRepo) RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Withdrawal, error) {
	row, err := r.queries.TemplateMarkWithdrawalRejected(ctx, sqlcdb.TemplateMarkWithdrawalRejectedParams{
		ID: id, RejectionReason: &reason, ReviewedBy: &reviewerID,
	})
	if err != nil {
		return nil, mapNoRows(err)
	}
	w := withdrawalFromSQLC(row)
	return &w, nil
}

// ===================== mapping helpers =====================

func categoryFromSQLC(r sqlcdb.TemplateCategory) Category {
	return Category{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func featureFromSQLC(r sqlcdb.TemplateFeature) Feature {
	return Feature{ID: r.ID, Name: r.Name, Value: deref(r.Value), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func templateFromSQLC(r sqlcdb.Template) Template {
	return Template{
		ID:               r.ID,
		AuthorID:         r.AuthorID,
		ProjectID:        r.ProjectID,
		Name:             r.Name,
		Slug:             r.Slug,
		Image:            r.Image,
		Video:            deref(r.Video),
		Spec:             rawOrEmptyObject(r.Spec),
		ShortDescription: r.ShortDescription,
		Description:      r.Description,
		OriginalPrice:    numericToString(r.OriginalPrice),
		Discount:         numericToString(r.Discount),
		DiscountType:     r.DiscountType,
		Views:            r.Views,
		Version:          r.Version,
		IsVerify:         r.IsVerify,
		DemoURL:          deref(r.DemoUrl),
		RequiredEnvs:     rawOrEmptyArray(r.RequiredEnvs),
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func submissionFromSQLC(r sqlcdb.TemplateSubmissionRequest) Submission {
	return Submission{
		ID:               r.ID,
		UserID:           r.UserID,
		ProjectID:        r.ProjectID,
		Name:             r.Name,
		ShortDescription: r.ShortDescription,
		Description:      r.Description,
		Image:            deref(r.Image),
		Video:            deref(r.Video),
		DemoURL:          deref(r.DemoUrl),
		OriginalPrice:    numericToString(r.OriginalPrice),
		Status:           r.Status,
		RejectionReason:  deref(r.RejectionReason),
		TemplateID:       r.TemplateID,
		ReviewedBy:       r.ReviewedBy,
		ReviewedAt:       r.ReviewedAt,
		RequiredEnvs:     rawOrEmptyArray(r.RequiredEnvs),
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func orderFromSQLC(r sqlcdb.TemplateOrder) Order {
	return Order{
		ID:             r.ID,
		UserID:         r.UserID,
		TemplateID:     r.TemplateID,
		Amount:         numericToString(r.Amount),
		Status:         r.Status,
		PaymentStatus:  r.PaymentStatus,
		CommissionPaid: r.CommissionPaid,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

func withdrawalFromSQLC(r sqlcdb.TemplateWithdrawalRequest) Withdrawal {
	return Withdrawal{
		ID:              r.ID,
		UserID:          r.UserID,
		Amount:          numericToString(r.Amount),
		CardNumber:      r.CardNumber,
		CardOwner:       r.CardOwner,
		Status:          r.Status,
		RejectionReason: deref(r.RejectionReason),
		ReviewedBy:      r.ReviewedBy,
		ReviewedAt:      r.ReviewedAt,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return database.ErrNotFound
	}
	return fmt.Errorf("query template: %w", err)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func rawOrEmptyObject(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func rawOrEmptyArray(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func rawOrNil(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// numericToString renders a pgtype.Numeric as a plain decimal string. An
// invalid/NULL value renders as "0".
func numericToString(n pgtype.Numeric) string {
	if !n.Valid {
		return "0"
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return "0"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// stringToNumeric parses a decimal string ("12.50") into a pgtype.Numeric for
// a money/NUMERIC(20,2) query param.
func stringToNumeric(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if s == "" {
		s = "0"
	}
	if err := n.ScanScientific(s); err != nil {
		return n, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return n, nil
}

// optionalNumeric is like stringToNumeric but treats "" as NULL/no-filter
// instead of an error - used for optional price-range query filters.
func optionalNumeric(s string) pgtype.Numeric {
	if s == "" {
		return pgtype.Numeric{}
	}
	n, err := stringToNumeric(s)
	if err != nil {
		return pgtype.Numeric{}
	}
	return n
}
