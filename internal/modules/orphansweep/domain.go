package orphansweep

import "context"

// Repository — orphan sweep uchun DB porti: hozir DB'da hali terminal bo'lmagan
// (deployment'i mavjud bo'lishi kerak) release/debug service_id'lari.
type Repository interface {
	ListLiveEngineServiceIDs(ctx context.Context) ([]string, error)
}
