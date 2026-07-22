package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LegacyNumericMigrationResult struct {
	LegacyID      int32
	ProjectID     uuid.UUID
	OwnerID       uuid.UUID
	OldDeployment string
	ServiceID     string
	WebhookID     uuid.UUID
}

// MigrateLegacyNumericRuntime performs an explicitly requested stop-first
// migration. It is deliberately not part of the automatic startup migrator:
// operators invoke it in small batches and verify capacity between batches.
func (o *KubernetesOrchestrator) MigrateLegacyNumericRuntime(
	ctx context.Context,
	legacyID int32,
	resolve func(context.Context, int32) (uuid.UUID, uuid.UUID, error),
	replaceServiceID func(context.Context, uuid.UUID, string, string, bool) (bool, error),
) (LegacyNumericMigrationResult, error) {
	projectID, ownerID, err := resolve(ctx, legacyID)
	if err != nil {
		return LegacyNumericMigrationResult{}, fmt.Errorf("resolve legacy project %d: %w", legacyID, err)
	}
	dep, webhookID, err := o.findActiveLegacyNumericDeployment(ctx, legacyID)
	if err != nil {
		return LegacyNumericMigrationResult{}, err
	}
	flow, schemas, modules, env, err := o.legacyRuntimeAssets(ctx, dep.Name)
	if err != nil {
		return LegacyNumericMigrationResult{}, err
	}

	// Stop first, as required for capacity safety. The stable Service remains
	// present but temporarily has no endpoints until the new child is ready.
	if err := o.scaleLegacyDeployment(ctx, dep.Name, 0, 60*time.Second); err != nil {
		return LegacyNumericMigrationResult{}, fmt.Errorf("stop %s: %w", dep.Name, err)
	}
	rollback := true
	defer func() {
		if rollback {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			_ = o.scaleLegacyDeployment(rollbackCtx, dep.Name, 1, 60*time.Second)
		}
	}()

	result, err := o.deployUserRuntime(ctx, DeploySpec{
		OwnerID:          ownerID,
		ProjectID:        projectID,
		RuntimeProjectID: strconv.FormatInt(int64(legacyID), 10),
		Debug:            false,
		Name:             dep.Name,
		Flow:             flow,
		Schemas:          schemas,
		Modules:          modules,
		Env:              env,
		WebhookUUID:      webhookID,
	})
	if err != nil {
		return LegacyNumericMigrationResult{}, fmt.Errorf("start owner runtime for %s: %w", dep.Name, err)
	}
	rollback = false // Service has atomically cut over; never revive the old pod.
	if replaced, replaceErr := replaceServiceID(ctx, projectID, dep.Name, result.ServiceID, false); replaceErr != nil {
		return LegacyNumericMigrationResult{}, fmt.Errorf("persist runtime service id for %s: %w", dep.Name, replaceErr)
	} else if !replaced {
		return LegacyNumericMigrationResult{}, fmt.Errorf("active release row for %s not found", dep.Name)
	}
	if err := o.deleteLegacyRuntimeAssets(ctx, dep.Name); err != nil {
		return LegacyNumericMigrationResult{}, fmt.Errorf("remove stopped legacy assets %s: %w", dep.Name, err)
	}
	return LegacyNumericMigrationResult{LegacyID: legacyID, ProjectID: projectID, OwnerID: ownerID, OldDeployment: dep.Name, ServiceID: result.ServiceID, WebhookID: webhookID}, nil
}

func (o *KubernetesOrchestrator) findActiveLegacyNumericDeployment(ctx context.Context, legacyID int32) (*metav1.PartialObjectMetadata, uuid.UUID, error) {
	deps, err := o.cs.AppsV1().Deployments(o.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=botmother-engine,project_id=" + strconv.FormatInt(int64(legacyID), 10)})
	if err != nil {
		return nil, uuid.Nil, err
	}
	for i := range deps.Items {
		dep := &deps.Items[i]
		if !strings.HasPrefix(dep.Name, "release-") || dep.Spec.Replicas == nil || *dep.Spec.Replicas < 1 {
			continue
		}
		webhookID, hookErr := o.legacyWebhookID(ctx, dep.Name)
		if hookErr == nil {
			return &metav1.PartialObjectMetadata{ObjectMeta: dep.ObjectMeta}, webhookID, nil
		}
	}
	return nil, uuid.Nil, fmt.Errorf("active legacy release for project %d not found", legacyID)
}

func (o *KubernetesOrchestrator) scaleLegacyDeployment(ctx context.Context, name string, replicas int32, timeout time.Duration) error {
	client := o.cs.AppsV1().Deployments(o.cfg.Namespace)
	dep, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if replicas == 0 && apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	dep.Spec.Replicas = &replicas
	if _, err := client.Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, getErr := client.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			if replicas == 0 && apierrors.IsNotFound(getErr) {
				return nil
			}
			return getErr
		}
		if replicas == 0 && current.Status.Replicas == 0 && current.Status.ReadyReplicas == 0 {
			return nil
		}
		if replicas > 0 && current.Status.ReadyReplicas >= replicas {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("deployment scale timeout")
}
