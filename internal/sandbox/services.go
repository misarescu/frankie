package sandbox

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Service is a sidecar for integration tests (FR-SB-9): it shares the pod's
// network, so steps reach it on localhost. Services run as native sidecars
// (init containers that keep running), so they start before, and are ready
// for, everything after them, and stop when the pod's work is done.
type Service struct {
	Name      string // DNS label; the container is svc-<name>
	Image     string
	Port      int // when set, later containers wait until it accepts TCP connections
	Env       map[string]string
	RunAsUser *int64 // the image's own non-root user; nil runs as the agent user
	DataDir   string // a writable emptyDir mounted here
	Limits    corev1.ResourceList
}

// Smallest share the pod's own containers keep after services take theirs.
var (
	minMainCPU    = resource.MustParse("250m")
	minMainMemory = resource.MustParse("512Mi")
)

// serviceDataSize bounds each service's data directory.
var serviceDataSize = resource.MustParse("2Gi")

func serviceContainerName(name string) string { return "svc-" + name }

func validateServices(services []Service) error {
	var errs []error
	seen := map[string]bool{}
	for i, s := range services {
		if msgs := validation.IsDNS1123Label(serviceContainerName(s.Name)); len(msgs) > 0 || s.Name == "" {
			errs = append(errs, fmt.Errorf("service %d: invalid name %q", i, s.Name))
		}
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("service %d: duplicate name %q", i, s.Name))
		}
		seen[s.Name] = true
		if s.Image == "" {
			errs = append(errs, fmt.Errorf("service %q: image is required", s.Name))
		}
		if s.Port < 0 || s.Port > 65535 {
			errs = append(errs, fmt.Errorf("service %q: port %d out of range", s.Name, s.Port))
		}
		if s.RunAsUser != nil && *s.RunAsUser == 0 {
			errs = append(errs, fmt.Errorf("service %q: must not run as root", s.Name))
		}
		for _, r := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			if _, ok := s.Limits[r]; !ok {
				errs = append(errs, fmt.Errorf("service %q: %s limit is required", s.Name, r))
			}
		}
	}
	return errors.Join(errs...)
}

// carve returns what the pod's own containers keep of limits once the services
// take their share, so a pod with services stays within its pod type's budget
// (docs/spec/09).
func carve(limits corev1.ResourceList, services []Service) (corev1.ResourceList, error) {
	main := limits.DeepCopy()
	for _, r := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		q := main[r]
		for _, s := range services {
			q.Sub(s.Limits[r])
		}
		main[r] = q
	}
	cpu, mem := main[corev1.ResourceCPU], main[corev1.ResourceMemory]
	if cpu.Cmp(minMainCPU) < 0 || mem.Cmp(minMainMemory) < 0 {
		return nil, fmt.Errorf("services leave %s CPU and %s memory of the pod's %s/%s; at least %s/%s must remain",
			cpu.String(), mem.String(), limits.Cpu().String(), limits.Memory().String(), minMainCPU.String(), minMainMemory.String())
	}
	return main, nil
}

// serviceContainers returns the native sidecars and the volumes they need.
func serviceContainers(services []Service) ([]corev1.Container, []corev1.Volume) {
	var containers []corev1.Container
	var volumes []corev1.Volume
	always := corev1.ContainerRestartPolicyAlways
	for _, s := range services {
		name := serviceContainerName(s.Name)
		sc := containerSecurity()
		sc.RunAsUser = s.RunAsUser
		c := corev1.Container{
			Name:            name,
			Image:           s.Image,
			RestartPolicy:   &always,
			Resources:       corev1.ResourceRequirements{Limits: s.Limits.DeepCopy(), Requests: s.Limits.DeepCopy()},
			SecurityContext: sc,
		}
		for _, k := range slices.Sorted(maps.Keys(s.Env)) {
			c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: s.Env[k]})
		}
		if s.Port > 0 {
			c.Ports = []corev1.ContainerPort{{ContainerPort: int32(s.Port)}}
			// The next container starts only once the service accepts connections.
			c.StartupProbe = &corev1.Probe{
				ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(int32(s.Port))}},
				PeriodSeconds:    2,
				FailureThreshold: 90,
			}
		}
		if s.DataDir != "" {
			vol := name + "-data"
			c.VolumeMounts = []corev1.VolumeMount{{Name: vol, MountPath: s.DataDir}}
			volumes = append(volumes, corev1.Volume{Name: vol, VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(serviceDataSize)},
			}})
		}
		containers = append(containers, c)
	}
	return containers, volumes
}
