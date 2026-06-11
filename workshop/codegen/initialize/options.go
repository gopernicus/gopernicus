// Package initialize is the project-bootstrap engine behind `gopernicus init`.
//
// It owns everything about creating a new project except interactivity:
// option types, flag parsing, non-interactive defaults, scaffolding, feature
// asset copying, and the dependency/tool-pin/generate orchestration. Front
// ends (the in-framework tool's flags-only init, the gopernicus CLI's
// interactive TUI init) resolve Options however they like and hand them to
// Run.
package initialize

import (
	"fmt"
	"strings"
)

// FeatureSelection tracks which framework features the user wants bootstrapped.
type FeatureSelection struct {
	Authentication bool
	Authorization  bool
	Tenancy        bool
	EventOutbox    bool
	JobQueue       bool
}

// AllFeatures returns a selection with everything enabled (the default).
func AllFeatures() FeatureSelection {
	return FeatureSelection{
		Authentication: true,
		Authorization:  true,
		Tenancy:        true,
		EventOutbox:    true, // transactional outbox for reliable event delivery
		JobQueue:       true, // durable deferred job processing with retry
	}
}

// NoFeatures returns a selection with everything disabled.
func NoFeatures() FeatureSelection {
	return FeatureSelection{}
}

// Any returns true if at least one feature is selected.
func (f FeatureSelection) Any() bool {
	return f.Authentication || f.Authorization || f.Tenancy || f.EventOutbox || f.JobQueue
}

// EnabledFor reports whether the feature with the given flag name
// ("authentication", "authorization", "tenancy", "event-outbox", "job-queue")
// is selected.
func (f FeatureSelection) EnabledFor(name string) bool {
	switch name {
	case "authentication":
		return f.Authentication
	case "authorization":
		return f.Authorization
	case "tenancy":
		return f.Tenancy
	case "event-outbox":
		return f.EventOutbox
	case "job-queue":
		return f.JobQueue
	}
	return false
}

// InfrastructureSelection tracks which infrastructure adapters to bootstrap.
type InfrastructureSelection struct {
	HasRedis        bool // Redis client (enables redis cache; required for Redis Streams)
	HasRedisStreams bool // Redis Streams event bus backend
	HasStorageDisk  bool // Local disk file storage
	HasStorageGCS   bool // Google Cloud Storage
	HasStorageS3    bool // AWS S3 / compatible
	HasSendGrid     bool // SendGrid email delivery
	HasTelemetry    bool // Telemetry stack (Jaeger)
}

// AICompanionSelection tracks which AI coding assistant to bootstrap.
type AICompanionSelection struct {
	Claude bool // CLAUDE.md + .claude/skills/
}

// DefaultAICompanion returns the default AI companion selection.
func DefaultAICompanion() AICompanionSelection {
	return AICompanionSelection{Claude: true}
}

// DefaultInfrastructure returns the default infrastructure selection.
// Redis + disk + GCS + SendGrid are on by default (matching the docker-compose setup).
func DefaultInfrastructure() InfrastructureSelection {
	return InfrastructureSelection{
		HasRedis:        true,
		HasRedisStreams: true,
		HasStorageDisk:  true,
		HasStorageGCS:   true,
		HasSendGrid:     true,
		HasTelemetry:    true,
	}
}

// Options holds all inputs for the init engine.
type Options struct {
	ProjectName      string
	OrgHint          string // extracted from "org/name" format (e.g. "jrazmi" from "jrazmi/foo")
	ModulePath       string
	FrameworkVersion string // gopernicus framework version (e.g. "v0.1.0"); "" means latest
	NoInteractive    bool
	FeaturesFlag     string // raw --features value; "" means use default (all)
	Features         FeatureSelection
	Infra            InfrastructureSelection
	AI               AICompanionSelection
}

// ParseArgs parses the init command's positional argument and flags
// (--module/-m, --no-interactive, --features, --framework-version) into
// Options. Selections are left unresolved; see ResolveDefaults.
func ParseArgs(args []string) (Options, error) {
	var opts Options
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--module" || args[i] == "-m":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--module requires a value")
			}
			i++
			opts.ModulePath = args[i]
		case args[i] == "--no-interactive":
			opts.NoInteractive = true
		case strings.HasPrefix(args[i], "--features="):
			opts.FeaturesFlag = strings.TrimPrefix(args[i], "--features=")
		case args[i] == "--features":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--features requires a value")
			}
			i++
			opts.FeaturesFlag = args[i]
		case strings.HasPrefix(args[i], "--framework-version="):
			opts.FrameworkVersion = strings.TrimPrefix(args[i], "--framework-version=")
		case args[i] == "--framework-version":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--framework-version requires a value")
			}
			i++
			opts.FrameworkVersion = args[i]
		case strings.HasPrefix(args[i], "--"):
			return opts, fmt.Errorf("unknown flag %q", args[i])
		default:
			if opts.ProjectName == "" {
				raw := args[i]
				// "org/repo" or "github.com/org/repo" → extract repo name and infer module path.
				if idx := strings.LastIndex(raw, "/"); idx >= 0 {
					opts.OrgHint = raw[:idx]
					opts.ProjectName = raw[idx+1:]
				} else {
					opts.ProjectName = raw
				}
			}
		}
	}
	return opts, nil
}

// ResolveDefaults fills in the non-interactive defaults: validates the
// project name, infers the module path, parses the --features flag (all
// features when omitted), and applies the default infrastructure and AI
// companion selections.
func ResolveDefaults(opts *Options) error {
	if opts.ProjectName == "" {
		return fmt.Errorf("project name required\n\nUsage: gopernicus init <project-name>")
	}
	if err := ValidateProjectName(opts.ProjectName); err != nil {
		return err
	}
	if opts.ModulePath == "" {
		if opts.OrgHint != "" {
			if strings.Contains(opts.OrgHint, ".") {
				opts.ModulePath = opts.OrgHint + "/" + opts.ProjectName
			} else {
				opts.ModulePath = "github.com/" + opts.OrgHint + "/" + opts.ProjectName
			}
		} else {
			opts.ModulePath = "github.com/your-org/" + opts.ProjectName
			fmt.Printf("note: using module path %q — edit go.mod to change it\n", opts.ModulePath)
		}
	}

	// Default: all features enabled unless --features flag was provided.
	if opts.FeaturesFlag != "" {
		features, err := ParseFeaturesFlag(opts.FeaturesFlag)
		if err != nil {
			return err
		}
		opts.Features = features
	} else {
		opts.Features = AllFeatures()
	}

	// Use defaults for infrastructure and AI companion in non-interactive mode.
	opts.Infra = DefaultInfrastructure()
	opts.AI = DefaultAICompanion()

	return nil
}

// DefaultModulePath suggests a module path from the org hint and project
// name, for front ends that prompt before resolving.
func DefaultModulePath(orgHint, projectName string) string {
	switch {
	case orgHint != "" && projectName != "":
		// Infer github.com/<org>/<repo> from "org/repo" shorthand.
		if strings.Contains(orgHint, ".") {
			return orgHint + "/" + projectName
		}
		return "github.com/" + orgHint + "/" + projectName
	case projectName != "":
		return "github.com/your-org/" + projectName
	}
	return ""
}

// ParseFeaturesFlag parses the --features flag value.
// Accepts: "none", "authentication", "authorization", "tenancy", "event-outbox", "job-queue", or comma-separated.
func ParseFeaturesFlag(value string) (FeatureSelection, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "none" {
		return NoFeatures(), nil
	}
	if value == "all" || value == "" {
		return AllFeatures(), nil
	}

	features := NoFeatures()
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "authentication":
			features.Authentication = true
		case "authorization":
			features.Authorization = true
		case "tenancy":
			features.Tenancy = true
		case "event-outbox":
			features.EventOutbox = true
		case "job-queue":
			features.JobQueue = true
		default:
			return features, fmt.Errorf("unknown feature %q (valid: authentication, authorization, tenancy, event-outbox, job-queue, none, all)", name)
		}
	}
	return features, nil
}

// ValidateProjectName checks that a project name is non-empty and uses only
// letters, numbers, hyphens, and underscores.
func ValidateProjectName(s string) error {
	if s == "" {
		return fmt.Errorf("required")
	}
	for _, c := range s {
		if !isAlphaNumDash(c) {
			return fmt.Errorf("only letters, numbers, and hyphens allowed")
		}
	}
	return nil
}

// ValidateModulePath checks that a module path is non-empty and has no spaces.
func ValidateModulePath(s string) error {
	if s == "" {
		return fmt.Errorf("required")
	}
	if strings.Contains(s, " ") {
		return fmt.Errorf("module path cannot contain spaces")
	}
	return nil
}

func isAlphaNumDash(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_'
}
