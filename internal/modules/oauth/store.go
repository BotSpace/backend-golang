// Package oauth implements the MCP OAuth 2.0 authorization server
// (authorization-code + PKCE, dynamic client registration) that MCP clients
// discover via /.well-known/oauth-authorization-server. It was migrated here
// from the legacy Django `web` service, whose SimpleJWT auth 500'd because it
// loaded the user by integer PK while Go-issued JWTs carry a UUID user_id.
package oauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a client or (valid, unexpired) code is absent.
var ErrNotFound = errors.New("oauth: not found")

// Client is a dynamically-registered public OAuth client (no secret).
type Client struct {
	ClientID                string
	ClientName              string
	RedirectURIs            []string
	GrantTypes              []string
	ResponseTypes           []string
	TokenEndpointAuthMethod string
	CreatedAt               time.Time
}

// AuthCode is a single-use authorization code bound to the approving user and
// the client's PKCE challenge.
type AuthCode struct {
	Code                string
	ClientID            string
	UserID              uuid.UUID
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	Resource            string
	ExpiresAt           time.Time
}

// Store persists OAuth clients and authorization codes. The usecase depends on
// this interface (not the concrete pgStore) so it can be faked in tests.
type Store interface {
	CreateClient(ctx context.Context, c *Client) error
	GetClient(ctx context.Context, clientID string) (*Client, error)
	SaveCode(ctx context.Context, a *AuthCode) error
	TakeCode(ctx context.Context, code string) (*AuthCode, error)
}

// pgStore is the Postgres-backed Store.
type pgStore struct {
	pool *pgxpool.Pool
}

// NewStore builds the Postgres-backed Store.
func NewStore(pool *pgxpool.Pool) Store {
	return &pgStore{pool: pool}
}

func (s *pgStore) CreateClient(ctx context.Context, c *Client) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_client
			(client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		c.ClientID, c.ClientName, c.RedirectURIs, c.GrantTypes, c.ResponseTypes, c.TokenEndpointAuthMethod,
	)
	return err
}

func (s *pgStore) GetClient(ctx context.Context, clientID string) (*Client, error) {
	var c Client
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method, created_at
		FROM oauth_client WHERE client_id = $1`, clientID,
	).Scan(&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.GrantTypes, &c.ResponseTypes, &c.TokenEndpointAuthMethod, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *pgStore) SaveCode(ctx context.Context, a *AuthCode) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_authorization_code
			(code, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, scope, resource, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.Code, a.ClientID, a.UserID, a.RedirectURI, a.CodeChallenge, a.CodeChallengeMethod, a.Scope, a.Resource, a.ExpiresAt,
	)
	return err
}

// TakeCode atomically fetches AND deletes a code (single-use). It returns
// ErrNotFound when the code is unknown or already expired — the caller must
// not distinguish the two (both are an invalid_grant to the client).
func (s *pgStore) TakeCode(ctx context.Context, code string) (*AuthCode, error) {
	var a AuthCode
	err := s.pool.QueryRow(ctx, `
		DELETE FROM oauth_authorization_code WHERE code = $1
		RETURNING code, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, scope, resource, expires_at`,
		code,
	).Scan(&a.Code, &a.ClientID, &a.UserID, &a.RedirectURI, &a.CodeChallenge, &a.CodeChallengeMethod, &a.Scope, &a.Resource, &a.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(a.ExpiresAt) {
		return nil, ErrNotFound
	}
	return &a, nil
}
