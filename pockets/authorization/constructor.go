package authorization

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/authorizer"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// Components contains independently usable decision, read and data-write services.
// Inbound access points authorize calls before invoking data writers.
type Components struct {
	TupleCache *tuplecache.TupleCache
	Decisions  *decisions.Service
	// Authorizer serves sdk/capabilities/authorization over Decisions: the
	// protocol's implementation of record. It has no global resource; a host
	// that maps global roles builds its own with authorizer.New and
	// authorizer.WithGlobalResource.
	Authorizer         *authorizer.Engine
	Relationships      *relationships.Service
	Roles              *roles.Service
	Mutations          *mutations.Service
	HTTP               *authorizationhttp.Adapter
	RelationshipWriter *relationships.RelationshipWriter
	RoleWriter         *roles.Writer
	// Audit is Repositories.Audit, the committed change history reader; nil
	// when unwired. The host controls access and retention.
	Audit   audit.Reader
	log     *slog.Logger
	posture auditPosture
}

// auditPosture is New's verdict on audit wiring, reported at registration.
type auditPosture struct {
	mode      environment.Mode
	recording bool
	reasons   string
	waived    bool
	observed  bool
}

// New validates host wiring and assembles the public services.
func New(repos Repositories, opts ...Option) (Components, error) {
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"Repositories.Tuples", repos.Tuples}, {"Repositories.Mutations", repos.Mutations}, {"Repositories.Operations", repos.Operations}, {"Repositories.Audit", repos.Audit}, {"Repositories.TupleSource", repos.TupleSource}, {"WithTupleCache", cfg.TupleBackend}} {
		if isTypedNil(dep.value) {
			return Components{}, fmt.Errorf("authorization: %s is typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	if repos.Tuples == nil {
		return Components{}, ErrNoKindConfigured
	}
	if repos.Operations != nil && repos.Mutations == nil {
		return Components{}, ErrOperationsWithoutMutations
	}
	if cfg.RoleRoutesGate != nil && repos.Mutations == nil {
		return Components{}, authorizationhttp.ErrRoleRoutesWithoutMutations
	}
	posture, err := checkAuditPosture(repos, cfg)
	if err != nil {
		return Components{}, err
	}
	if err := authorizationhttp.ValidateListStrategy(cfg.ListStrategy); err != nil {
		return Components{}, err
	}
	posture.observed = cfg.DecisionObserver != nil
	comps := Components{Audit: repos.Audit, log: cfg.Logger, posture: posture}
	if comps.log == nil {
		comps.log = slog.Default()
	}
	posture.warn(comps.log)
	decisionOptions := []decisions.Option{decisions.WithLogger(comps.log), decisions.WithLimits(cfg.Limits), decisions.WithDiagnosticObserver(cfg.DiagnosticObserver), decisions.WithDecisionObserver(cfg.DecisionObserver), decisions.WithTupleCache(cfg.TupleBackend, repos.TupleSource, cfg.TuplePolicy)}
	if cfg.ModelOption != nil {
		decisionOptions = append(decisionOptions, cfg.ModelOption)
	}
	comps.Decisions, err = decisions.NewService(repos.Tuples, decisionOptions...)
	if err != nil {
		return Components{}, err
	}
	comps.TupleCache = comps.Decisions.TupleCache()
	comps.Authorizer, err = authorizer.New(comps.Decisions)
	if err != nil {
		return Components{}, err
	}
	roleOptions := []roles.Option{}
	if comps.TupleCache != nil {
		roleOptions = append(roleOptions, roles.WithSnapshots(comps.TupleCache))
	}
	comps.Roles, err = roles.NewService(repos.Tuples, roleOptions...)
	if err != nil {
		return Components{}, err
	}
	comps.RoleWriter, err = roles.NewWriter(repos.Tuples, roles.WithValidator(comps.Decisions.CompiledModel()))
	if err != nil {
		return Components{}, err
	}
	parts, err := relationships.NewService(repos.Tuples, relationships.WithValidator(comps.Decisions.CompiledModel()))
	if err != nil {
		return Components{}, err
	}
	comps.Relationships, comps.RelationshipWriter = parts.Service, parts.RelationshipWriter
	if repos.Mutations != nil {
		comps.Mutations, err = mutations.NewService(repos.Mutations, mutations.WithModel(comps.Decisions.CompiledModel()), mutations.WithLimits(cfg.Limits), mutations.WithLogger(comps.log), mutations.WithLedger(repos.Operations))
		if err != nil {
			return Components{}, err
		}
	}
	var roleWriter authorizationhttp.RoleWriter
	if comps.Mutations != nil {
		roleWriter = comps.Mutations
	}

	var decision authorizationhttp.DecisionService
	if comps.Decisions != nil {
		decision = comps.Decisions
	}
	var roleReader authorizationhttp.RoleReader
	if comps.Roles != nil {
		roleReader = comps.Roles
	}
	comps.HTTP, err = authorizationhttp.New(
		authorizationhttp.Services{Decisions: decision, Roles: roleReader, Mutations: roleWriter},
		authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
			Gate:         cfg.RoleRoutesGate,
			WritePolicy:  cfg.RoleWritePolicy,
			ListStrategy: cfg.ListStrategy,
		}),
		authorizationhttp.WithDecisionObserver(cfg.DecisionObserver),
		authorizationhttp.WithLogger(comps.log),
	)
	if err != nil {
		return Components{}, err
	}
	return comps, nil
}

// Register mounts only the configured bundled HTTP routes.
func (c Components) Register(m pockets.Mount) error {
	if c.HTTP == nil {
		return fmt.Errorf("authorization: components are not initialized: %w", sdk.ErrInvalidInput)
	}
	c.log.Info("registered authorization pocket", "relationships", c.Relationships != nil, "roles", c.Roles != nil, "model", c.Decisions.CompiledModel() != nil, "baseline_relationship_writes", c.RelationshipWriter != nil, "role_routes", c.HTTP.RoutesEnabled(), "audit_recording", c.posture.recording, "audit_reader", c.Audit != nil, "decision_observer", c.posture.observed, "runtime_mode", c.posture.mode.String())
	return c.HTTP.Register(m.Router)
}

// checkAuditPosture validates a non-zero mode and the audit wiring production
// requires. The zero mode only rejects a contradictory acknowledgement.
func checkAuditPosture(repos Repositories, cfg config) (auditPosture, error) {
	posture := auditPosture{mode: cfg.RuntimeMode, recording: true}
	if posture.mode != "" {
		if err := environment.ValidateMode(posture.mode); err != nil {
			return auditPosture{}, fmt.Errorf("authorization: WithRuntimeMode: %w", err)
		}
	}
	var reasons []string
	for _, port := range []struct {
		name  string
		value any
	}{{"Repositories.Tuples", repos.Tuples}, {"Repositories.Mutations", repos.Mutations}} {
		if port.value == nil {
			continue
		}
		reporter, ok := port.value.(audit.RecordingReporter)
		switch {
		case !ok:
			posture.recording = false
			reasons = append(reasons, port.name+" does not report AuditRecording() (stores pgx < v0.19.0 / turso < v0.18.0, or a host wrapper hides it — forward the method)")
		case !reporter.AuditRecording():
			posture.recording = false
			reasons = append(reasons, port.name+" reports AuditRecording() false (construct the store WithAudit())")
		}
	}
	if cfg.AuditDisabledAcknowledged && posture.recording {
		return auditPosture{}, ErrAuditAcknowledgedWithRecording
	}
	if repos.Audit == nil {
		reasons = append(reasons, "Repositories.Audit is nil (wire the store's audit reader)")
	}
	if posture.mode == "" || len(reasons) == 0 {
		return posture, nil
	}
	posture.reasons = strings.Join(reasons, "; ")
	switch {
	case cfg.AuditDisabledAcknowledged:
		posture.waived = true
	case posture.mode.IsProduction() && posture.recording:
		return auditPosture{}, fmt.Errorf("%w: %s", ErrAuditRequired, posture.reasons)
	case posture.mode.IsProduction():
		return auditPosture{}, fmt.Errorf("%w: %s; to run without recording pass WithAuditDisabledAcknowledged()", ErrAuditRequired, posture.reasons)
	}
	return posture, nil
}

// warn logs one line for a waived requirement or, in development, an unmet one,
// and in production one line when no decision observer records denials.
func (p auditPosture) warn(log *slog.Logger) {
	switch {
	case p.reasons == "":
	case p.waived:
		log.Warn("authorization audit requirement waived", "posture_waiver", "AuditDisabledAcknowledged", "runtime_mode", p.mode.String(), "reason", p.reasons)
	default:
		log.Warn("authorization audit wiring would be refused in production", "runtime_mode", p.mode.String(), "reason", p.reasons)
	}
	if p.mode.IsProduction() && !p.observed {
		log.Warn("authorization: denials are not durably recorded; wire WithDecisionObserver", "runtime_mode", p.mode.String())
	}
}

func isTypedNil(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
