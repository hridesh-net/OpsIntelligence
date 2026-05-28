package tuibridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ── Public step DSL ───────────────────────────────────────────────────────

// WizardStep is one entry in a wizard. Exactly one of Form or SideEffect must
// be non-nil. Skip, when non-nil, returns true to silently bypass the step.
type WizardStep struct {
	Icon     string
	Title    string
	Subtitle string

	// Form: returns a fresh form spec (re-invoked each time the step activates
	// so it can pull current state). OnSubmit applies the user's answers; the
	// raw decoded fields are passed through so the caller can typecheck per-key.
	Form     func() WizardFormSpec
	OnSubmit func(fields map[string]any) error

	// SideEffect: runs in a goroutine while the spinner shows Label.
	SideEffect      func() error
	SideEffectLabel string

	Skip func() bool
}

// WizardFormSpec describes the form rendered for a single step.
type WizardFormSpec struct {
	Fields []WizardFieldSpec
}

// WizardFieldSpec describes one field; use the constructors below.
type WizardFieldSpec struct {
	Kind        string             `json:"kind"`
	Key         string             `json:"key,omitempty"`
	Label       string             `json:"label,omitempty"`
	Description string             `json:"description,omitempty"`
	Placeholder string             `json:"placeholder,omitempty"`
	Default     string             `json:"default,omitempty"`
	DefaultBool bool               `json:"-"`
	DefaultList []string           `json:"-"`
	Password    bool               `json:"password,omitempty"`
	Options     []WizardOptionSpec `json:"options,omitempty"`
	Affirmative string             `json:"affirmative,omitempty"`
	Negative    string             `json:"negative,omitempty"`
	Title       string             `json:"title,omitempty"`
}

// WizardOptionSpec is one choice in a Select / MultiSelect.
type WizardOptionSpec struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field constructors keep the call sites readable.

func WizardInput(key, label, description, defaultValue string) WizardFieldSpec {
	return WizardFieldSpec{Kind: "input", Key: key, Label: label, Description: description, Default: defaultValue}
}

func WizardPassword(key, label, description string) WizardFieldSpec {
	return WizardFieldSpec{Kind: "input", Key: key, Label: label, Description: description, Password: true}
}

func WizardSelect(key, label, description, defaultValue string, opts []WizardOptionSpec) WizardFieldSpec {
	return WizardFieldSpec{Kind: "select", Key: key, Label: label, Description: description, Default: defaultValue, Options: opts}
}

func WizardMultiSelect(key, label, description string, defaultValues []string, opts []WizardOptionSpec) WizardFieldSpec {
	return WizardFieldSpec{Kind: "multi_select", Key: key, Label: label, Description: description, DefaultList: defaultValues, Options: opts}
}

func WizardConfirm(key, label, description string, defaultYes bool, yes, no string) WizardFieldSpec {
	return WizardFieldSpec{Kind: "confirm", Key: key, Label: label, Description: description, DefaultBool: defaultYes, Affirmative: yes, Negative: no}
}

func WizardNote(title, description string) WizardFieldSpec {
	return WizardFieldSpec{Kind: "note", Title: title, Description: description}
}

// ── Runner ────────────────────────────────────────────────────────────────

// WizardOptions configures RunWizard.
type WizardOptions struct {
	Brand   string
	Steps   []WizardStep
	LogDir  string
	OnDone  string // optional summary shown on the "done" screen
}

// RunWizard launches the embedded Rust TUI in wizard mode and walks through
// the supplied steps. Each form is sent as a `wizard.step` notification; the
// Rust side replies with `wizard.submit` carrying the user's answers, which
// are passed to the step's OnSubmit callback. Side-effect steps run in a
// goroutine while a spinner is shown.
func RunWizard(ctx context.Context, opts WizardOptions) error {
	// Map of pending form id → channel awaiting submit result.
	type submitMsg struct {
		fields    map[string]any
		cancelled bool
	}
	var (
		mu       sync.Mutex
		nextID   uint64
		pending  = make(map[uint64]chan submitMsg)
	)
	quit := make(chan struct{})
	requestQuit := func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	}

	handler := func(msg Message) {
		switch msg.Method {
		case "view.exit":
			requestQuit()
		case "wizard.submit":
			var p struct {
				ID        uint64          `json:"id"`
				Fields    json.RawMessage `json:"fields"`
				Cancelled bool            `json:"cancelled"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				return
			}
			fields := map[string]any{}
			if len(p.Fields) > 0 {
				_ = json.Unmarshal(p.Fields, &fields)
			}
			mu.Lock()
			ch, ok := pending[p.ID]
			delete(pending, p.ID)
			mu.Unlock()
			if ok {
				ch <- submitMsg{fields: fields, cancelled: p.Cancelled}
			}
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
	defer func() { _ = b.Close(2 * time.Second) }()

	if err := b.Send("view.push", map[string]any{
		"view": "wizard",
		"wizard": map[string]any{
			"brand": opts.Brand,
		},
	}); err != nil {
		return err
	}

	// Push the full step plan so the wizard view can render a sidebar
	// progress tracker. Only form steps appear in the sidebar — side-effect
	// steps run "inside" the active form context and shouldn't be listed.
	// Consecutive form steps that share the same Title are collapsed into a
	// single sidebar group; `step_num` records the 1-based form-step number at
	// which the group becomes active so the Rust side can highlight correctly.
	// Compute total form steps for the progress header. Skipped steps are
	// excluded so the denominator matches the runtime form counter.
	totalForms := 0
	for i := range opts.Steps {
		s := &opts.Steps[i]
		if s.Form == nil {
			continue
		}
		if s.Skip != nil && s.Skip() {
			continue
		}
		totalForms++
	}

	planItems := make([]map[string]any, 0, len(opts.Steps))
	var lastTitle string
	formNum := uint32(0)
	for i := range opts.Steps {
		st := &opts.Steps[i]
		if st.Form == nil {
			continue
		}
		// Skipped steps must not consume a form-number, otherwise the
		// plan's `step_num` drifts ahead of the runtime counter and the
		// sidebar highlights the wrong group.
		if st.Skip != nil && st.Skip() {
			continue
		}
		formNum++
		if len(planItems) > 0 && st.Title == lastTitle {
			continue
		}
		lastTitle = st.Title
		planItems = append(planItems, map[string]any{
			"icon":     st.Icon,
			"title":    st.Title,
			"step_num": formNum,
		})
	}
	_ = b.Send("wizard.plan", map[string]any{"steps": planItems})

	formNum = 0
	for i := range opts.Steps {
		select {
		case <-quit:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		step := &opts.Steps[i]
		if step.Skip != nil && step.Skip() {
			continue
		}

		switch {
		case step.Form != nil:
			formNum++
			id := nextIDLocked(&mu, &nextID)
			ch := make(chan submitMsg, 1)
			mu.Lock()
			pending[id] = ch
			mu.Unlock()
			spec := step.Form()
			if err := b.Send("wizard.step", buildFormStep(id, step, spec, uint32(formNum), uint32(totalForms))); err != nil {
				return err
			}
			select {
			case <-quit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-b.Done():
				return b.Err()
			case res := <-ch:
				if res.cancelled {
					requestQuit()
					return nil
				}
				if step.OnSubmit != nil {
					if err := step.OnSubmit(res.fields); err != nil {
						return err
					}
				}
			}
		case step.SideEffect != nil:
			if err := b.Send("wizard.step", map[string]any{
				"kind":       "side",
				"icon":       step.Icon,
				"title":      step.Title,
				"subtitle":   step.Subtitle,
				"step_num":   uint32(formNum),
				"step_total": uint32(totalForms),
				"label":      step.SideEffectLabel,
			}); err != nil {
				return err
			}
			errCh := make(chan error, 1)
			go func() { errCh <- step.SideEffect() }()
			var sideErr error
			select {
			case <-quit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case sideErr = <-errCh:
			}
			if sideErr != nil {
				_ = b.Send("wizard.step", map[string]any{
					"kind":       "side",
					"icon":       step.Icon,
					"title":      step.Title,
					"subtitle":   step.Subtitle,
					"step_num":   uint32(formNum),
					"step_total": uint32(totalForms),
					"label":      step.SideEffectLabel,
					"error":      sideErr.Error(),
				})
				return sideErr
			}
		}
	}

	// Done screen.
	_ = b.Send("wizard.step", map[string]any{
		"kind":    "done",
		"message": opts.OnDone,
	})
	select {
	case <-quit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.Done():
		return b.Err()
	}
}

func nextIDLocked(mu *sync.Mutex, n *uint64) uint64 {
	mu.Lock()
	*n++
	id := *n
	mu.Unlock()
	return id
}

func buildFormStep(id uint64, step *WizardStep, spec WizardFormSpec, num, total uint32) map[string]any {
	fields := make([]map[string]any, 0, len(spec.Fields))
	for _, f := range spec.Fields {
		m := map[string]any{"kind": f.Kind}
		if f.Key != "" {
			m["key"] = f.Key
		}
		if f.Label != "" {
			m["label"] = f.Label
		}
		if f.Description != "" {
			m["description"] = f.Description
		}
		switch f.Kind {
		case "input":
			m["placeholder"] = f.Placeholder
			m["default"] = f.Default
			m["password"] = f.Password
		case "select":
			m["default"] = f.Default
			m["options"] = f.Options
		case "multi_select":
			m["default"] = f.DefaultList
			m["options"] = f.Options
		case "confirm":
			m["default"] = f.DefaultBool
			aff := f.Affirmative
			if aff == "" {
				aff = "Yes"
			}
			neg := f.Negative
			if neg == "" {
				neg = "No"
			}
			m["affirmative"] = aff
			m["negative"] = neg
		case "note":
			if f.Title != "" {
				m["title"] = f.Title
			}
		}
		fields = append(fields, m)
	}
	return map[string]any{
		"kind":       "form",
		"id":         id,
		"icon":       step.Icon,
		"title":      step.Title,
		"subtitle":   step.Subtitle,
		"step_num":   num,
		"step_total": total,
		"fields":     fields,
	}
}

// ── Field-value typed accessors ───────────────────────────────────────────

// WizardString extracts a string field from a submit map, with fallback.
func WizardString(fields map[string]any, key, fallback string) string {
	if v, ok := fields[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

// WizardBool extracts a bool field with fallback.
func WizardBool(fields map[string]any, key string, fallback bool) bool {
	if v, ok := fields[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}

// WizardStrings extracts a []string field with fallback.
func WizardStrings(fields map[string]any, key string, fallback []string) []string {
	if v, ok := fields[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, x := range arr {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return fallback
}

// Unused for now but kept so callers can rely on a consistent interface.
var _ = fmt.Sprintf
