// pack_agent.go wires the SoyaPack manifest layer to the kernel registry —
// the second leg of EPIC 1 Stream B (APP-541). RegisterFromPack turns a
// validated *soyapack.Manifest plus the on-disk Pack directory into a
// kernel.Agent.
//
// Two prompt-body shapes are supported (mutually exclusive in the
// manifest, enforced upstream by soyapack.Validate):
//
//   - m.Entry              — single system prompt (the v0 default).
//   - m.Prompt.Steps[]     — N-stage prompt chain (APP-550 Compo Phase B).
//     The kernel runs each stage with the previous stage's *full*
//     response fed in as the user message of the next stage; only the
//     final stage's stream is forwarded to the caller. This is how Compo
//     reaches its grade-school-ready output quality without sacrificing
//     the OpenAI-Compat streaming surface.
//
// The handler resolves the BYOK upstream via llmcall.ResolveConfig
// (manifest.prompt.upstream  >  env SOYA_MODEL_*  >  defaults). The
// upstream model id is rewritten to the resolved real model so the
// caller's virtual id (`soya:<slug>`) never leaks to the upstream.
//
// This is the path EstateMuse, NewsBeam, Compo, SilentCut all take in
// later stages: write a soyapack.yaml + one or more prompt files,
// point the kernel at the directory, and the Agent shows up at GET
// /v1/agents.
package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// providerFactory builds an llmcall.Provider from a resolved Config. The
// indirection exists so unit tests can substitute a fake provider — see
// pack_agent_test.go. Production callers never touch this; RegisterFromPack
// falls back to llmcall.OpenAICompat when the factory is nil.
type providerFactory func(cfg llmcall.Config) llmcall.Provider

// RegisterFromPack registers an Agent built from a SoyaPack manifest plus
// its on-disk source directory. The manifest is assumed to have already
// passed soyapack.Validate — this function does not re-run validation,
// it only checks the structural pre-conditions it needs to wire up the
// Handler (entry or prompt.steps present, expose.virtual_model_id present).
//
// packDir must be an absolute path to the directory containing
// soyapack.yaml; m.Entry / m.Prompt.Steps[*].Prompt are resolved relative
// to it.
func (k *Kernel) RegisterFromPack(m *soyapack.Manifest, packDir string) error {
	return k.registerFromPack(m, packDir, nil)
}

// registerFromPack is the implementation seam: tests pass a custom
// providerFactory to avoid spinning up a real HTTP upstream. Production
// callers go through RegisterFromPack which threads nil here.
func (k *Kernel) registerFromPack(m *soyapack.Manifest, packDir string, factory providerFactory) error {
	if m == nil {
		return fmt.Errorf("kernel: RegisterFromPack: nil manifest")
	}
	hasSteps := m.Prompt != nil && len(m.Prompt.Steps) > 0
	if m.Entry == "" && !hasSteps {
		return fmt.Errorf("kernel: pack %q missing entry / prompt.steps", m.Name)
	}
	if m.Entry != "" && hasSteps {
		// soyapack.Validate already rejects this combination, but the
		// kernel is the last guard before runtime — refuse to register
		// rather than silently picking one shape.
		return fmt.Errorf("kernel: pack %q has both entry and prompt.steps (mutually exclusive)", m.Name)
	}
	if m.Expose == nil || m.Expose.VirtualModelID == "" {
		return fmt.Errorf("kernel: pack %q missing expose.virtual_model_id", m.Name)
	}
	slug := strings.TrimPrefix(m.Expose.VirtualModelID, VirtualModelPrefix)
	if slug == "" || slug == m.Expose.VirtualModelID {
		// Either the prefix was absent or the id was just "soya:". Either
		// way the surface contract is broken; refuse to register rather
		// than silently dropping the request later.
		return fmt.Errorf("kernel: pack %q expose.virtual_model_id %q must be of the form soya:<slug>",
			m.Name, m.Expose.VirtualModelID)
	}

	// Load every system prompt body up front. Disk reads on every chat
	// call would be wasteful and would also race with a Pack uninstall.
	// We cache the bodies at register time and never re-read them.
	prompts, err := loadPromptBodies(m, packDir)
	if err != nil {
		return err
	}

	// Manifest-level prompt.upstream wins over env SOYA_MODEL_*; see
	// llmcall.ResolveConfig and APP-543. Nil decl is the same as
	// LoadConfigFromEnv so packs that don't pin an upstream still work.
	var decl *soyapack.UpstreamDecl
	if m.Prompt != nil {
		decl = m.Prompt.Upstream
	}
	cfg := llmcall.ResolveConfig(decl)

	var provider llmcall.Provider
	if factory != nil {
		provider = factory(cfg)
	} else {
		provider = llmcall.OpenAICompat{Cfg: cfg}
	}

	handler := buildPackHandler(prompts, provider, cfg.Model)

	k.Register(Agent{
		Slug:        slug,
		Description: m.Description,
		Handler:     handler,
		Manifest:    m,
	})

	// --- wire manifest.actions[] (DD-010 EstateMuse — APP-553) -----------
	//
	// Each ActionDecl gets its prompt file loaded at register time and
	// installed as a per-Pack handler indexed by (slug, action_id). The
	// handler treats the prompt body as the system message and the
	// (row_id + payload) as the user message — that's the contract
	// EstateMuse's "every row has a generate-post button" leans on.
	//
	// A missing prompt file is fatal: the Pack declared it would respond
	// to this action, so silently registering a stub would leave the
	// HTTP route 200-but-broken.
	if err := k.registerPackActions(m, packDir, slug, provider, cfg.Model); err != nil {
		return err
	}

	// --- best-effort: wire storage_nas[] (DD-011 SilentCut — APP-554) ----
	//
	// Same posture as channels[] / schedules[]: an unwired NASHook is a
	// best-effort skip, not a register-time fatal. The Agent must remain
	// chat-reachable even when its NAS target is unresolved (e.g. the
	// operator hasn't set ${SOYA_NAS_HOST} yet).
	k.resolveNASTargets(m, slug)

	// --- best-effort: wire channels[] + schedules[] ----------------------
	//
	// Both are intentionally non-fatal: an Agent must remain reachable
	// via the chat surface even when its outbound channel or scheduler
	// is unwired. Operators see warnings via the host-installed logger;
	// the kernel itself just logs and moves on.
	scheduleHook, channelHook, logger := k.getHooks()

	publishers := resolveChannelPublishers(m, channelHook, logger)

	// Schedules — fire == run the Agent's Handler with an empty user
	// turn, then push the final body through every wired channel
	// publisher. The handler already streams the final stage; we
	// collect the stream into a single string before publishing.
	for i, schedDecl := range m.Schedules {
		if scheduleHook == nil {
			logger("kernel: pack %q schedules[%d] declared but no ScheduleHook wired — skipping", m.Name, i)
			continue
		}
		jobID := fmt.Sprintf("pack:%s:%d", slug, i)
		spec := ScheduleSpec{
			Cron:           schedDecl.Cron,
			Once:           schedDecl.Once,
			TZ:             schedDecl.TZ,
			IdempotencyKey: schedDecl.IdempotencyKey,
			MissedFire:     schedDecl.MissedFire,
			Payload:        schedDecl.Payload,
		}
		fire := makeScheduledFire(slug, handler, publishers, m.Description, logger)
		if err := scheduleHook(jobID, spec, fire); err != nil {
			logger("kernel: pack %q schedules[%d] hook error: %v — skipping", m.Name, i, err)
		}
	}

	return nil
}

// resolveChannelPublishers asks the host's ChannelHook for an outbound
// publisher per declared channel. Failures are logged and skipped so a
// missing env var (or unwired hook) cannot prevent the Pack from being
// chat-reachable.
func resolveChannelPublishers(
	m *soyapack.Manifest,
	hook ChannelHook,
	logger func(string, ...any),
) []ChannelPublisher {
	if len(m.Channels) == 0 {
		return nil
	}
	if hook == nil {
		if len(m.Channels) > 0 {
			logger("kernel: pack %q declares %d channel(s) but no ChannelHook wired — outbound disabled",
				m.Name, len(m.Channels))
		}
		return nil
	}
	out := make([]ChannelPublisher, 0, len(m.Channels))
	for i, ch := range m.Channels {
		pub, err := hook(ChannelBindingSpec{
			Kind:      ch.Kind,
			BindingID: ch.BindingID,
			Secrets:   ch.Secrets,
		})
		if err != nil {
			logger("kernel: pack %q channels[%d] (kind=%s binding=%s): %v — skipping",
				m.Name, i, ch.Kind, ch.BindingID, err)
			continue
		}
		if pub == nil {
			continue
		}
		out = append(out, pub)
	}
	return out
}

// makeScheduledFire wraps the Agent's Handler so it can be triggered
// autonomously by the scheduler. There is no caller-supplied user
// message — the system prompt(s) alone must produce a useful response.
// The collected stream is pushed through every wired ChannelPublisher
// (DingTalk for now); failures per channel are logged and never
// propagate to the scheduler (which would otherwise treat them as a
// missed fire and retry-storm).
func makeScheduledFire(
	slug string,
	handler Handler,
	publishers []ChannelPublisher,
	title string,
	logger func(string, ...any),
) func(ctx context.Context) {
	return func(ctx context.Context) {
		out := make(chan llmcall.Chunk, 8)
		errCh := make(chan error, 1)
		go func() {
			errCh <- handler(ctx, auth.Identity{Subject: "system:scheduler"}, llmcall.Request{
				Model:    VirtualModelPrefix + slug,
				Messages: nil, // scheduled trigger — system prompts alone seed the chain
				Stream:   true,
			}, out)
			close(out)
		}()
		var sb strings.Builder
		for c := range out {
			if c.Done {
				continue
			}
			sb.WriteString(c.Delta)
		}
		if err := <-errCh; err != nil {
			logger("kernel: scheduled fire for soya:%s handler error: %v", slug, err)
			return
		}
		body := sb.String()
		if body == "" {
			logger("kernel: scheduled fire for soya:%s produced empty body — not publishing", slug)
			return
		}
		for i, pub := range publishers {
			if err := pub.Send(ctx, title, body); err != nil {
				logger("kernel: scheduled fire for soya:%s channel[%d] send failed: %v", slug, i, err)
			}
		}
	}
}

// promptBody pairs a step id with its pre-loaded system prompt. For the
// single-prompt (m.Entry) case the list has exactly one entry whose id
// is empty.
type promptBody struct {
	id   string
	body string
}

// loadPromptBodies resolves and reads every prompt file the Pack
// declares (entry or prompt.steps[]). Returns the ordered list of
// (stepID, body) pairs the handler will run.
func loadPromptBodies(m *soyapack.Manifest, packDir string) ([]promptBody, error) {
	if m.Entry != "" {
		body, err := os.ReadFile(filepath.Join(packDir, m.Entry))
		if err != nil {
			return nil, fmt.Errorf("kernel: read pack %q entry %s: %w", m.Name, m.Entry, err)
		}
		return []promptBody{{id: "", body: string(body)}}, nil
	}
	out := make([]promptBody, 0, len(m.Prompt.Steps))
	for _, step := range m.Prompt.Steps {
		body, err := os.ReadFile(filepath.Join(packDir, step.Prompt))
		if err != nil {
			return nil, fmt.Errorf("kernel: read pack %q step %q prompt %s: %w",
				m.Name, step.ID, step.Prompt, err)
		}
		out = append(out, promptBody{id: step.ID, body: string(body)})
	}
	return out, nil
}

// buildPackHandler turns the resolved prompt bodies + provider into a
// kernel.Handler. The single-prompt case uses the original streaming
// shape; the multi-step case runs N-1 non-streaming generations
// followed by a streaming final stage.
func buildPackHandler(prompts []promptBody, provider llmcall.Provider, resolvedModel string) Handler {
	return func(ctx context.Context, _ auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
		if len(prompts) <= 1 {
			// Single-prompt path: prepend the system prompt; preserve
			// everything else the caller sent. The upstream model id
			// is rewritten to the resolved real model; a caller-
			// supplied "soya:<slug>" virtual id would otherwise hit
			// the upstream verbatim and 400.
			systemPrompt := ""
			if len(prompts) == 1 {
				systemPrompt = prompts[0].body
			}
			injected := make([]llmcall.Message, 0, len(req.Messages)+1)
			if systemPrompt != "" {
				injected = append(injected, llmcall.Message{Role: "system", Content: systemPrompt})
			}
			injected = append(injected, req.Messages...)
			return provider.GenerateStream(ctx, llmcall.Request{
				Model:          resolvedModel,
				Messages:       injected,
				Temperature:    req.Temperature,
				MaxTokens:      req.MaxTokens,
				Stream:         true,
				ResponseFormat: req.ResponseFormat,
			}, out)
		}

		// Multi-step chain. Stages 1..N-1 run non-streaming; their full
		// response becomes the user message of the next stage. Stage N
		// streams. We preserve the caller's original conversation as
		// the user message of stage 1, then collapse to a single
		// "the previous stage said: ..." user message thereafter so
		// each stage sees only what the chain explicitly threads.
		userPayload := combineUserMessages(req.Messages)

		// Chain stages need a max_tokens *floor*, not just a default. The
		// caller's max_tokens controls the *final* user-visible output
		// length — short for a chat ("give me a quick answer"), longer
		// for a structured artifact. But chain intermediate stages are
		// an internal contract: each stage emits a structured artifact
		// (YAML report, guide.v1 JSON, …) the next stage consumes, and
		// it must finish.
		//
		// Reasoning models (DashScope qwen3.x, OpenAI o-series, …) burn
		// 3-5 KB of *reasoning* tokens before the first content token.
		// A 1024-token cap is exhausted during reasoning and DashScope
		// tears down the TCP stream without a stop frame — we observe
		// "unexpected EOF" at the SSE scanner. 4096 leaves enough
		// headroom for reasoning + a 2 KB structured artifact.
		//
		// We apply the same floor to the final stage too: a 1024 cap
		// would truncate the refined guide.v1 JSON into invalid syntax,
		// and "the chat output got cut off" is worse than "the chat is
		// slightly longer than I picked." Power users who actually want
		// short output can pick a smaller value AND it still wins above
		// the floor — but the floor stays at 4096.
		const chainMinMaxTokens = 4096
		stageMaxTokens := req.MaxTokens
		if stageMaxTokens < chainMinMaxTokens {
			stageMaxTokens = chainMinMaxTokens
		}

		for i := 0; i < len(prompts)-1; i++ {
			stage := prompts[i]
			// Chain intermediate stages run streaming internally and
			// collect the chunks. A non-streaming call would force the
			// upstream to generate the whole response *before* sending
			// any headers — Compo's generate_guide stage emits 2-3 KB
			// of JSON and routinely takes 60-120s on DashScope, which
			// trips http.Transport.ResponseHeaderTimeout. Streaming
			// makes headers arrive in ~1-2s and shifts the timeout
			// pressure onto the per-chunk gap, which the upstream paces.
			content, err := streamCollect(ctx, provider, llmcall.Request{
				Model: resolvedModel,
				Messages: []llmcall.Message{
					{Role: "system", Content: stage.body},
					{Role: "user", Content: userPayload},
				},
				Temperature:    req.Temperature,
				MaxTokens:      stageMaxTokens,
				Stream:         true,
				ResponseFormat: req.ResponseFormat,
			})
			if err != nil {
				return fmt.Errorf("kernel: prompt step %q (#%d): %w", stage.id, i, err)
			}
			userPayload = content
		}

		// Final stage. We could pipe upstream chunks straight to `out`
		// for true token-by-token streaming, but a transient EOF from
		// the upstream would then arrive at the client mid-stream with
		// no way to retry — half the JSON, no closing brace. Collect-
		// then-emit lets streamCollect's retry layer recover from
		// upstream tear-downs, at the cost of "burst" streaming. The
		// front-end can choose to simulate per-token streaming locally.
		final := prompts[len(prompts)-1]
		content, err := streamCollect(ctx, provider, llmcall.Request{
			Model: resolvedModel,
			Messages: []llmcall.Message{
				{Role: "system", Content: final.body},
				{Role: "user", Content: userPayload},
			},
			Temperature:    req.Temperature,
			MaxTokens:      stageMaxTokens,
			Stream:         true,
			ResponseFormat: req.ResponseFormat,
		})
		if err != nil {
			return fmt.Errorf("kernel: prompt step %q (#%d): %w", final.id, len(prompts)-1, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- llmcall.Chunk{Delta: content}:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- llmcall.Chunk{Done: true}:
		}
		return nil
	}
}

// resolveNASTargets walks manifest.storage_nas[] and asks the wired
// NASHook to produce a NASTarget per entry. Failures are logged and
// skipped — never fatal — so a missing env var (or unwired hook)
// cannot block agent registration. (DD-011 SilentCut — APP-554)
func (k *Kernel) resolveNASTargets(m *soyapack.Manifest, slug string) {
	if len(m.StorageNAS) == 0 {
		return
	}
	hook := k.getNASHook()
	_, _, logger := k.getHooks()
	if hook == nil {
		logger("kernel: pack %q declares %d storage_nas target(s) but no NASHook wired — NAS writes disabled",
			m.Name, len(m.StorageNAS))
		return
	}
	for i, decl := range m.StorageNAS {
		id := decl.ID
		if id == "" {
			id = "primary"
		}
		target, err := hook(NASBindingSpec{
			ID:       id,
			Protocol: decl.Protocol,
			HostRef:  decl.HostRef,
			Share:    decl.Share,
			Access:   decl.Access,
			Secrets:  decl.Secrets,
		})
		if err != nil {
			logger("kernel: pack %q storage_nas[%d] (id=%s protocol=%s): %v — skipping",
				m.Name, i, id, decl.Protocol, err)
			continue
		}
		if target.Handle == nil {
			logger("kernel: pack %q storage_nas[%d] (id=%s) hook returned nil handle — skipping",
				m.Name, i, id)
			continue
		}
		if target.ID == "" {
			target.ID = id
		}
		if target.Protocol == "" {
			target.Protocol = decl.Protocol
		}
		if target.BasePath == "" {
			target.BasePath = decl.Share
		}
		k.storeNASTarget(slug, target)
	}
}

// registerPackActions reads each ActionDecl's handler prompt off disk
// and installs a per-Pack ActionHandler that runs the prompt against
// the configured upstream model. The handler returns a synchronous
// "done" ActionResult — alpha doesn't queue, it streams the upstream
// LLM and folds the body into ActionResult.Output["content"]. Later
// stages will move to a real task queue.
func (k *Kernel) registerPackActions(m *soyapack.Manifest, packDir, slug string, provider llmcall.Provider, resolvedModel string) error {
	for i, decl := range m.Actions {
		if decl.Handler == "" {
			return fmt.Errorf("kernel: pack %q actions[%d] (id=%q) missing handler", m.Name, i, decl.ID)
		}
		body, err := os.ReadFile(filepath.Join(packDir, decl.Handler))
		if err != nil {
			return fmt.Errorf("kernel: read pack %q actions[%d] (id=%q) handler %s: %w",
				m.Name, i, decl.ID, decl.Handler, err)
		}
		k.RegisterPackAction(slug, decl.ID, buildPackActionHandler(string(body), provider, resolvedModel))
	}
	return nil
}

// buildPackActionHandler wraps the prompt body + upstream provider into
// an ActionHandler. The action's prompt is the system message; the user
// message is a JSON-encoded { row_id, payload } object so the prompt
// author can reference it directly. The full streamed response is
// collected into ActionResult.Output["content"].
func buildPackActionHandler(promptBody string, provider llmcall.Provider, resolvedModel string) ActionHandler {
	return func(ctx context.Context, decl soyapack.ActionDecl, req ActionRequest) (ActionResult, error) {
		userPayload, err := encodeActionUserPayload(req.RowID, req.Payload)
		if err != nil {
			return ActionResult{}, fmt.Errorf("kernel: encode action payload: %w", err)
		}
		content, err := streamCollect(ctx, provider, llmcall.Request{
			Model: resolvedModel,
			Messages: []llmcall.Message{
				{Role: "system", Content: promptBody},
				{Role: "user", Content: userPayload},
			},
			Stream: true,
		})
		if err != nil {
			return ActionResult{}, fmt.Errorf("kernel: action %q upstream: %w", decl.ID, err)
		}
		return ActionResult{
			TaskID:    newTaskID(),
			Status:    "done",
			AgentSlug: req.AgentSlug,
			ActionID:  decl.ID,
			RowID:     req.RowID,
			Output: map[string]any{
				"content":   content,
				"handler":   decl.Handler,
				"artifacts": decl.Artifacts,
			},
			EnqueuedAt: time.Now(),
		}, nil
	}
}

// streamCollect runs a streaming generation against the upstream and
// returns the concatenated content. Used by both chain intermediate
// stages and the per-row action handler — anywhere we want the full
// response as a single string while still getting headers back from
// the upstream in seconds rather than waiting for the whole body to
// land. Mirrors the SSE consumer pattern so a Done chunk terminates
// cleanly without contributing to the buffer.
//
// Retries io.ErrUnexpectedEOF up to streamCollectMaxRetries times. Long
// SSE streams to DashScope reasoning models (qwen3.6-plus thinks for
// 60-90s before any content) get torn down by intermediate network
// hops or the upstream's own idle handling, surfacing as a half-closed
// TCP — there's no semantic error, just retry. We deliberately do NOT
// retry status-code errors or context cancellation: a 4xx means the
// request is bad, a 5xx means the upstream is down for everyone, and
// a cancelled ctx means the caller wants to stop.
func streamCollect(ctx context.Context, provider llmcall.Provider, req llmcall.Request) (string, error) {
	var lastErr error
	for attempt := 0; attempt < streamCollectMaxRetries; attempt++ {
		s, err := streamCollectOnce(ctx, provider, req)
		if err == nil {
			return s, nil
		}
		lastErr = err
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", fmt.Errorf("after %d retries: %w", streamCollectMaxRetries, lastErr)
}

const streamCollectMaxRetries = 3

func streamCollectOnce(ctx context.Context, provider llmcall.Provider, req llmcall.Request) (string, error) {
	out := make(chan llmcall.Chunk, 16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- provider.GenerateStream(ctx, req, out)
		close(out)
	}()
	var sb strings.Builder
	for c := range out {
		if c.Done {
			continue
		}
		sb.WriteString(c.Delta)
	}
	if err := <-errCh; err != nil {
		return "", err
	}
	return sb.String(), nil
}

// encodeActionUserPayload produces the user-message body the action's
// prompt sees: a small JSON object pinning the row id and the caller-
// supplied payload. JSON is the contract because (a) the prompt author
// can reference fields by name (b) downstream code can re-parse and
// audit the exact bytes the LLM saw.
func encodeActionUserPayload(rowID string, payload map[string]any) (string, error) {
	envelope := map[string]any{
		"row_id":  rowID,
		"payload": payload,
	}
	buf, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// combineUserMessages collapses the caller's message history into the
// single user payload that seeds stage 1 of a prompt chain. We keep
// only `user` and `assistant` turns; system messages from the caller
// are dropped (the chain owns the system role).
func combineUserMessages(messages []llmcall.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if msg.Content == "" {
			continue
		}
		if msg.Role == "assistant" {
			parts = append(parts, "Previous assistant turn: "+msg.Content)
			continue
		}
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n\n")
}
