package sandbox

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func postgresService() Service {
	uid := int64(999)
	return Service{
		Name: "postgres", Image: "postgres:17", Port: 5432, RunAsUser: &uid, DataDir: "/var/lib/postgresql/data",
		Env:    map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust", "PGDATA": "/var/lib/postgresql/data/pgdata"},
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
	}
}

func TestAgentPodWithServices(t *testing.T) {
	s := validSpec()
	s.Services = []Service{postgresService()}
	pod, err := AgentPod(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("init containers = %d", len(pod.Spec.InitContainers))
	}
	svc := pod.Spec.InitContainers[0]

	t.Run("runs as a native sidecar the runner can wait on", func(t *testing.T) {
		if svc.Name != "svc-postgres" || svc.RestartPolicy == nil || *svc.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			t.Errorf("sidecar = %s, restart %v", svc.Name, svc.RestartPolicy)
		}
		if svc.StartupProbe == nil || svc.StartupProbe.TCPSocket == nil || svc.StartupProbe.TCPSocket.Port.IntValue() != 5432 {
			t.Errorf("startup probe = %+v", svc.StartupProbe)
		}
	})
	t.Run("is locked down and non-root", func(t *testing.T) {
		sc := svc.SecurityContext
		if *sc.RunAsUser != 999 || *sc.AllowPrivilegeEscalation || sc.Capabilities.Drop[0] != "ALL" {
			t.Errorf("security = %+v", sc)
		}
		for _, e := range svc.Env {
			if strings.Contains(e.Name, "PROXY") {
				t.Errorf("service gets egress setting %s; it needs no network", e.Name)
			}
		}
	})
	t.Run("its limits come out of the pod's budget", func(t *testing.T) {
		runner := pod.Spec.Containers[0].Resources.Limits
		total := runner.Memory().DeepCopy()
		total.Add(*svc.Resources.Limits.Memory())
		if runner.Memory().String() != "2560Mi" || total.String() != "3Gi" {
			t.Errorf("runner memory %s, pod total %s; want 2560Mi and the pod type's 3Gi", runner.Memory(), total.String())
		}
		if runner.Cpu().String() != "1500m" {
			t.Errorf("runner cpu %s, want 1500m", runner.Cpu())
		}
	})
	t.Run("gets a writable data directory", func(t *testing.T) {
		if len(svc.VolumeMounts) != 1 || svc.VolumeMounts[0].MountPath != "/var/lib/postgresql/data" {
			t.Errorf("mounts = %+v", svc.VolumeMounts)
		}
		found := false
		for _, v := range pod.Spec.Volumes {
			found = found || (v.Name == svc.VolumeMounts[0].Name && v.EmptyDir != nil)
		}
		if !found {
			t.Error("data volume missing")
		}
	})
}

func TestGatePodStartsServicesBeforeTheCheckout(t *testing.T) {
	s := gateSpec()
	s.Services = []Service{postgresService()}
	pod, err := GatePod(s)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range pod.Spec.InitContainers {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "svc-postgres,clone,check-0,check-1" {
		t.Errorf("init containers = %s", got)
	}
	if mem := pod.Spec.InitContainers[1].Resources.Limits.Memory(); mem.String() != "2560Mi" {
		t.Errorf("commands get %s memory, want the pod's 3Gi minus 512Mi", mem)
	}
}

func TestServicesRejected(t *testing.T) {
	root := int64(0)
	big := postgresService()
	big.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2800Mi")}
	tests := []struct {
		name     string
		services []Service
		want     string
	}{
		{"leaves too little for the step", []Service{big}, "must remain"},
		{"root", []Service{func() Service { s := postgresService(); s.RunAsUser = &root; return s }()}, "must not run as root"},
		{"duplicate", []Service{postgresService(), postgresService()}, "duplicate name"},
		{"no limits", []Service{func() Service { s := postgresService(); s.Limits = nil; return s }()}, "limit is required"},
		{"bad name", []Service{func() Service { s := postgresService(); s.Name = "Main_DB"; return s }()}, "invalid name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			s.Services = tt.services
			if _, err := AgentPod(s); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
