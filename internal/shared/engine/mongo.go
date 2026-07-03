package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDatabaseProvisioner is the real MongoProvisioner for this backend's
// CMS design. Unlike the old Django system (one shared Mongo database + a
// `_project_id` tenant field, provisioned via create_mongo_users.py), this
// Go backend gives every project its own Mongo database — MongoDB creates
// databases/collections lazily on first write (internal/modules/cms/mongo.go
// already relies on that), so there is no per-project user/auth to
// provision, only a deterministic name to allocate. Provision is therefore a
// pure name computation; Deprovision does the one genuinely destructive
// action, dropping that database, which needs a real client.
type MongoDatabaseProvisioner struct {
	client *mongo.Client
	uri    string
}

// NewMongoDatabaseProvisioner dials APP_MONGO_URL — the same env var
// internal/modules/cms.NewMongoClient uses — and verifies connectivity.
func NewMongoDatabaseProvisioner(ctx context.Context) (*MongoDatabaseProvisioner, error) {
	uri := os.Getenv("APP_MONGO_URL")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(dialCtx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}
	if err := client.Ping(dialCtx, nil); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}
	return &MongoDatabaseProvisioner{client: client, uri: uri}, nil
}

// Close disconnects the underlying client. Call during graceful shutdown.
func (p *MongoDatabaseProvisioner) Close(ctx context.Context) error {
	return p.client.Disconnect(ctx)
}

// ProjectMongoDatabaseName is the deterministic per-project database name —
// kept identical to internal/modules/cms usecase.resolveMongoDB's fallback so
// a project provisioned here and one that fell back organically always agree.
func ProjectMongoDatabaseName(projectID uuid.UUID) string {
	return "cms_" + strings.ReplaceAll(projectID.String(), "-", "")
}

func (p *MongoDatabaseProvisioner) Provision(_ context.Context, projectID uuid.UUID) (MongoCredentials, error) {
	name := ProjectMongoDatabaseName(projectID)
	// No explicit "create database" call: Mongo creates it lazily on first
	// write. Touching a throwaway collection here would only leave clutter.
	return MongoCredentials{Database: name, ConnectionString: p.uri}, nil
}

func (p *MongoDatabaseProvisioner) Deprovision(ctx context.Context, projectID uuid.UUID) error {
	name := ProjectMongoDatabaseName(projectID)
	if err := p.client.Database(name).Drop(ctx); err != nil {
		return fmt.Errorf("drop mongo database %s: %w", name, err)
	}
	return nil
}
