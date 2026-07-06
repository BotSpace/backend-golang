// Package k8sx — backend kubernetes_service.py'ning worker'ga kerakli qismi:
// deployment (+ secret/configmap/service) o'chirish va engine podlarini ko'rish.
package k8sx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// cs is a kubernetes.Interface (not the concrete *kubernetes.Clientset) so
// tests can inject k8s.io/client-go/kubernetes/fake instead of talking to a
// real cluster.
type Client struct {
	cs        kubernetes.Interface
	Namespace string
	log       *zap.Logger
}

func New(namespace string, log *zap.Logger) (*Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Cluster tashqarisida (local dev) — kubeconfig fallback.
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return NewWithClient(cs, namespace, log), nil
}

// NewWithClient wires a pre-built clientset — used by tests to inject
// k8s.io/client-go/kubernetes/fake.
func NewWithClient(cs kubernetes.Interface, namespace string, log *zap.Logger) *Client {
	return &Client{cs: cs, Namespace: namespace, log: log}
}

// RemoveDeployment — backend remove_deployment bilan bir xil tartib:
// deployment + <name>-env secret + <name>-flow configmap + label bo'yicha service.
// NotFound xatolar e'tiborsiz (allaqachon yo'q).
func (c *Client) RemoveDeployment(ctx context.Context, name string) error {
	ignoreNotFound := func(err error, what string) error {
		if err == nil || apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("%s: %w", what, err)
	}

	if err := ignoreNotFound(
		c.cs.AppsV1().Deployments(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{}),
		"delete deployment",
	); err != nil {
		return err
	}
	c.log.Info("removed deployment", zap.String("name", name))

	if err := ignoreNotFound(
		c.cs.CoreV1().Secrets(c.Namespace).Delete(ctx, name+"-env", metav1.DeleteOptions{}),
		"delete secret",
	); err != nil {
		c.log.Warn("secret delete", zap.Error(err))
	}
	if err := ignoreNotFound(
		c.cs.CoreV1().ConfigMaps(c.Namespace).Delete(ctx, name+"-flow", metav1.DeleteOptions{}),
		"delete configmap",
	); err != nil {
		c.log.Warn("configmap delete", zap.Error(err))
	}

	svcs, err := c.cs.CoreV1().Services(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "deployment=" + name,
	})
	if err != nil {
		c.log.Warn("service list", zap.Error(err))
		return nil
	}
	for _, svc := range svcs.Items {
		if err := ignoreNotFound(
			c.cs.CoreV1().Services(c.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{}),
			"delete service",
		); err != nil {
			c.log.Warn("service delete", zap.String("name", svc.Name), zap.Error(err))
		}
	}
	return nil
}

// EnginePods — app=botmother-engine label'li podlar.
func (c *Client) EnginePods(ctx context.Context) ([]corev1.Pod, error) {
	pods, err := c.cs.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=botmother-engine",
	})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// CrashRestartThreshold — shuncha restartdan keyin pod "haqiqatan yiqilgan" sanaladi.
const CrashRestartThreshold = 5

// FailureGracePeriod — pod shundan yosh bo'lsa hali initializing/image pulling
// deb hisoblanadi va HECH QACHON o'chirilmaydi, restart count yoki waiting
// reason nima bo'lishidan qat'i nazar. Yangi boshlangan botga (masalan katta
// image pull qilinayotgan) o'zini ko'rsatishga vaqt beradi.
const FailureGracePeriod = 10 * time.Minute

var failedWaitingReasons = map[string]bool{
	"CrashLoopBackOff":  true,
	"Error":             true,
	"RunContainerError": true,
	"ImagePullBackOff":  true,
	"ErrImagePull":      true,
}

// IsCrashlooping — backend _is_crashlooping bilan bir xil mezon (restart
// count >= threshold + hozirgi holat waiting/failed), ustiga yosh podlar
// uchun grace davri qo'shilgan: pod FailureGracePeriod'dan yosh bo'lsa,
// qancha restart bo'lishidan qat'i nazar hali "muvaffaqiyatsiz" sanalmaydi.
func IsCrashlooping(pod corev1.Pod, now time.Time) bool {
	if now.Sub(pod.CreationTimestamp.Time) < FailureGracePeriod {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount < CrashRestartThreshold {
			continue
		}
		if cs.State.Waiting != nil && failedWaitingReasons[cs.State.Waiting.Reason] {
			return true
		}
	}
	return false
}

// EngineKind — pod label'idan aniqlangan bot-engine turi.
type EngineKind int

const (
	EngineKindNone EngineKind = iota
	EngineKindRelease
	EngineKindDebug
)

// BotEngineName — pod label'idagi "engine-name"dan release/debug deployment
// nomini va turini qaytaradi ("" / EngineKindNone = bot-engine emas — masalan
// module-* shared-module pod, u HECH QACHON tegilmaydi).
func BotEngineName(pod corev1.Pod) (string, EngineKind) {
	name := pod.Labels["engine-name"]
	switch {
	case strings.HasPrefix(name, "release-"):
		return name, EngineKindRelease
	case strings.HasPrefix(name, "debug-"):
		return name, EngineKindDebug
	default:
		return "", EngineKindNone
	}
}

// EngineFailure — CleanupCrashLoopingEngines o'chirgan bitta bot-engine.
type EngineFailure struct {
	Name string
	Kind EngineKind
}

// CleanupCrashLoopingEngines — cluster-driven tozalash: app=botmother-engine
// label'li podlarni ko'radi, har bir "engine-name" (release-*/debug-*, boshqa
// hech narsa — module-* ham) uchun IsCrashlooping mezoni bilan
// muvaffaqiyatsizni aniqlaydi va RemoveDeployment bilan to'liq resurs
// to'plamini (Deployment+Secret+ConfigMap+Service) o'chiradi.
//
// Bu qaror DB'dan MUSTAQIL — faqat pod'ning o'z real holatiga qaraydi, shuning
// uchun qaysi DB (eski Django yoki yangi Go backend) release/debug yozuvini
// kuzatib turishidan qat'i nazar orphan bot'larni ham ushlaydi.
//
// Bitta engine'ning o'chirilishi muvaffaqiyatsiz bo'lsa (masalan API xatosi),
// shu engine o'tkazib yuboriladi va qolganlar davom etadi — bitta yomon
// o'chirish butun tozalashni to'xtatmaydi.
func (c *Client) CleanupCrashLoopingEngines(ctx context.Context, now time.Time) ([]EngineFailure, error) {
	pods, err := c.EnginePods(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var removed []EngineFailure
	for _, pod := range pods {
		name, kind := BotEngineName(pod)
		if name == "" || kind == EngineKindNone || seen[name] || !IsCrashlooping(pod, now) {
			continue
		}
		seen[name] = true

		if err := c.RemoveDeployment(ctx, name); err != nil {
			c.log.Error("cleanup crashlooping engine: remove", zap.String("engine", name), zap.Error(err))
			continue
		}
		removed = append(removed, EngineFailure{Name: name, Kind: kind})
	}
	return removed, nil
}
