package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// API is the wire protocol a provider natively serves and a harness speaks to
// the LLM gateway (docs/spec/04).
type API string

// APIs.
const (
	APIAnthropic API = "anthropic" // Anthropic Messages
	APIOpenAI    API = "openai"    // OpenAI-compatible chat completions
)

// providerAPIs are the providers a model ref may name, by their LiteLLM
// provider name, and the API each natively serves. A harness may use a
// provider only if it speaks that API, so the gateway never translates.
var providerAPIs = map[string]API{
	"anthropic":  APIAnthropic,
	"openai":     APIOpenAI,
	"openrouter": APIOpenAI,
}

// Provider is a provider profile (docs/spec/04): a provider the platform's LLM
// gateway routes to, and the model ids it serves there. The credentials,
// base URL and limits live in the gateway's configuration, never here.
type Provider struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// Providers are the provider profiles of a deployment.
type Providers []Provider

// Validate checks every provider is known and unique and serves at least one
// model, and that no model id is served twice: the gateway serves a model
// under its id without the provider, so ids must be unique across providers.
func (ps Providers) Validate() error {
	var errs []error
	seenProvider := map[string]bool{}
	seenModel := map[string]string{}
	for i, p := range ps {
		path := fmt.Sprintf("providers[%d]", i)
		if _, ok := providerAPIs[p.Name]; !ok {
			errs = append(errs, fmt.Errorf("%s.name: unknown provider %q (known: %s)", path, p.Name, strings.Join(knownProviders(), ", ")))
		}
		if seenProvider[p.Name] {
			errs = append(errs, fmt.Errorf("%s.name: provider %q listed twice", path, p.Name))
		}
		seenProvider[p.Name] = true
		if len(p.Models) == 0 {
			errs = append(errs, fmt.Errorf("%s.models: a provider serves at least one model", path))
		}
		for j, m := range p.Models {
			if !modelRefRE.MatchString(p.Name + "/" + m) {
				errs = append(errs, fmt.Errorf("%s.models[%d]: %q is not a model id", path, j, m))
			}
			if other, dup := seenModel[m]; dup {
				errs = append(errs, fmt.Errorf("%s.models[%d]: model %q is also served by %s", path, j, m, other))
			}
			seenModel[m] = p.Name
		}
	}
	return errors.Join(errs...)
}

// Serves reports whether a provider profile serves the model ref.
func (ps Providers) Serves(ref string) bool {
	provider, model, ok := strings.Cut(ref, "/")
	if !ok {
		return false
	}
	i := slices.IndexFunc(ps, func(p Provider) bool { return p.Name == provider })
	return i >= 0 && slices.Contains(ps[i].Models, model)
}

// GatewayAPIs maps every served model, by the name the gateway serves it
// under, to the API its provider serves.
func (ps Providers) GatewayAPIs() map[string]API {
	out := map[string]API{}
	for _, p := range ps {
		for _, m := range p.Models {
			out[m] = providerAPIs[p.Name]
		}
	}
	return out
}

// GatewayModel is the name the LLM gateway serves a model ref under: the ref
// without its provider ("openrouter/openai/gpt-5" is served as "openai/gpt-5").
// A name without a provider is returned unchanged.
func GatewayModel(ref string) string {
	_, name, found := strings.Cut(ref, "/")
	if !found {
		return ref
	}
	return name
}

// CheckModels checks a resolved workflow against the deployment's provider
// profiles (FR-WF-5): every agent step's model (the step's, else its role's
// default, else defaultModel) is served by a configured provider. Whether the
// harness can use the provider is checked when the definition is parsed.
func CheckModels(w Workflow, roles map[string]Role, defaultModel string, ps Providers) error {
	var errs []error
	check := func(path string, a Agent) {
		ref, from := a.Model, "model"
		if ref == "" {
			ref, from = roles[a.RoleName()].DefaultModel, "role "+a.RoleName()+" default model"
		}
		if ref == "" {
			ref, from = defaultModel, "platform default model"
		}
		if !ps.Serves(ref) {
			errs = append(errs, fmt.Errorf("%s.agent: %s %s is not served by this platform's providers (%s)", path, from, ref, ps.describe()))
			return
		}
		if err := harnessCanUse(a.Harness, ref); err != nil {
			errs = append(errs, fmt.Errorf("%s.agent: %s: %w", path, from, err))
		}
	}
	for i, s := range w.Steps {
		path := fmt.Sprintf("steps[%d]", i)
		switch s.Kind() {
		case StepAgent:
			check(path, *s.Agent)
		case StepParallel:
			for j, b := range s.Parallel {
				check(fmt.Sprintf("%s.parallel[%d]", path, j), *b.Agent)
			}
		}
	}
	return errors.Join(errs...)
}

// harnessCanUse reports whether a harness speaks the API of a model ref's provider.
func harnessCanUse(harness, ref string) error {
	provider, _, _ := strings.Cut(ref, "/")
	api, ok := providerAPIs[provider]
	if !ok {
		return fmt.Errorf("unknown provider %q (known: %s)", provider, strings.Join(knownProviders(), ", "))
	}
	if h, ok := harnesses[harness]; ok && !slices.Contains(h.apis, api) {
		return fmt.Errorf("harness %s cannot use provider %q (it serves the %s API; the harness speaks %s)", harness, provider, api, joinAPIs(h.apis))
	}
	return nil
}

func (ps Providers) describe() string {
	if len(ps) == 0 {
		return "none configured"
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Name + ": " + strings.Join(p.Models, ", ")
	}
	return strings.Join(parts, "; ")
}

func knownProviders() []string {
	names := make([]string, 0, len(providerAPIs))
	for n := range providerAPIs {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func joinAPIs(apis []API) string {
	s := make([]string, len(apis))
	for i, a := range apis {
		s[i] = string(a)
	}
	return strings.Join(s, ", ")
}
