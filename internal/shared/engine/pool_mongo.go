package engine

import "os"

// PoolMongoConfig is the backend CMS destination, not a project's environment
// override or the legacy engine Mongo address. It is also used during repair of
// specs persisted before conversation analytics required a canonical identity.
type PoolMongoConfig struct {
	URI      string
	Database string
}

func CanonicalPoolMongoConfig(database string) PoolMongoConfig {
	uri := os.Getenv("APP_MONGO_URL")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	} // Same default as cms.NewMongoClient.
	if override := os.Getenv("APP_ENGINE_MONGO_DB_NAME"); override != "" {
		database = override
	}
	if database == "" {
		database = "botspace_cms"
	}
	return PoolMongoConfig{URI: uri, Database: database}
}

func (c PoolMongoConfig) Apply(env map[string]string) {
	env["APP_MONGO_URL"] = c.URI
	env["MONGO_ADDR"] = c.URI
	env["APP_ENGINE_MONGO_DB_NAME"] = c.Database
	env["MONGO_DB_NAME"] = c.Database
}
