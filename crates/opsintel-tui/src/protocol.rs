// The Rust side defines the full JSON-RPC envelope and many request payload
// structs that are produced *only* on the Go side and deserialized here, or
// vice versa. Several types therefore look "dead" to the compiler — they're
// kept for symmetric round-trip semantics with internal/tuibridge/protocol.go.
#![allow(dead_code)]

use serde::{Deserialize, Serialize};
use serde_json::Value;

pub const JSONRPC_VERSION: &str = "2.0";

/// Wire-level JSON-RPC 2.0 message. Sent as one line of newline-delimited JSON.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub jsonrpc: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub method: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub params: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorObject>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ErrorObject {
    pub code: i32,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
}

impl Message {
    pub fn notification(method: impl Into<String>, params: Value) -> Self {
        Self {
            jsonrpc: JSONRPC_VERSION.into(),
            id: None,
            method: Some(method.into()),
            params: Some(params),
            result: None,
            error: None,
        }
    }

    pub fn request(id: u64, method: impl Into<String>, params: Value) -> Self {
        Self {
            jsonrpc: JSONRPC_VERSION.into(),
            id: Some(id),
            method: Some(method.into()),
            params: Some(params),
            result: None,
            error: None,
        }
    }

    pub fn response(id: u64, result: Value) -> Self {
        Self {
            jsonrpc: JSONRPC_VERSION.into(),
            id: Some(id),
            method: None,
            params: None,
            result: Some(result),
            error: None,
        }
    }

    pub fn error(id: u64, code: i32, message: impl Into<String>) -> Self {
        Self {
            jsonrpc: JSONRPC_VERSION.into(),
            id: Some(id),
            method: None,
            params: None,
            result: None,
            error: Some(ErrorObject {
                code,
                message: message.into(),
                data: None,
            }),
        }
    }
}

// ─────────────────────────────────────────────
// Typed payloads — kept in sync with internal/tuibridge/protocol.go
// ─────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct TokenUsage {
    #[serde(default)]
    pub prompt_tokens: u64,
    #[serde(default)]
    pub completion_tokens: u64,
    #[serde(default)]
    pub total_tokens: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ViewPushParams {
    pub view: String,
    #[serde(default)]
    pub repl: Option<ReplViewState>,
    #[serde(default)]
    pub dashboard: Option<DashboardState>,
    #[serde(default)]
    pub repos: Option<ReposState>,
    #[serde(default)]
    pub doctor: Option<DoctorState>,
    #[serde(default)]
    pub monitor: Option<MonitorState>,
    #[serde(default)]
    pub wizard: Option<WizardState>,
}

// ── Wizard / form engine ─────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardState {
    #[serde(default)]
    pub brand: String, // e.g. "OPSINTELLIGENCE"
}

/// Sent once after `view.push` with the full step list so the wizard view
/// can render a sidebar progress tracker. The Rust side updates the active
/// index when each `wizard.step` arrives.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardPlan {
    #[serde(default)]
    pub steps: Vec<WizardPlanItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardPlanItem {
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub title: String,
    /// 1-based form-step number at which this sidebar group becomes active.
    /// When Go collapses consecutive same-titled steps into one group, this
    /// records the first step in the run so the Rust side can highlight the
    /// correct group as `active_step_num` advances.
    #[serde(default)]
    pub step_num: u32,
}

/// Sent by Go as `wizard.step` to instruct the Rust side to render one step
/// (either a form or a side-effect spinner). The Rust side replies with a
/// matching id via `wizard.submit` (form) or implicitly via Go pushing the next
/// step (side effect — Rust just renders the spinner until next push).
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum WizardStep {
    Form(WizardForm),
    Side(WizardSide),
    Done(WizardDone),
}

/// Payload for the "Setup complete" page. All fields are optional; the
/// renderer skips empty sections. `message` is kept for legacy callers that
/// only want a single free-form line under the headline.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardDone {
    #[serde(default)]
    pub headline: String,
    #[serde(default)]
    pub subline: String,
    #[serde(default)]
    pub summary: Vec<WizardDonePair>,
    #[serde(default)]
    pub next: Vec<WizardDonePair>,
    #[serde(default)]
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardDonePair {
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub value: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardForm {
    pub id: u64,
    pub icon: String,
    pub title: String,
    #[serde(default)]
    pub subtitle: String,
    pub step_num: u32,
    pub step_total: u32,
    pub fields: Vec<WizardField>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardSide {
    pub icon: String,
    pub title: String,
    #[serde(default)]
    pub subtitle: String,
    pub step_num: u32,
    pub step_total: u32,
    pub label: String,
    /// Optional error message if the side effect already failed before push.
    #[serde(default)]
    pub error: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum WizardField {
    Input {
        key: String,
        label: String,
        #[serde(default)]
        description: String,
        #[serde(default)]
        placeholder: String,
        #[serde(default)]
        default: String,
        #[serde(default)]
        password: bool,
    },
    Select {
        key: String,
        label: String,
        #[serde(default)]
        description: String,
        options: Vec<WizardOption>,
        #[serde(default)]
        default: String,
    },
    MultiSelect {
        key: String,
        label: String,
        #[serde(default)]
        description: String,
        options: Vec<WizardOption>,
        #[serde(default)]
        default: Vec<String>,
    },
    Confirm {
        key: String,
        label: String,
        #[serde(default)]
        description: String,
        #[serde(default)]
        default: bool,
        #[serde(default = "default_affirmative")]
        affirmative: String,
        #[serde(default = "default_negative")]
        negative: String,
    },
    Note {
        #[serde(default)]
        title: String,
        description: String,
    },
}

fn default_affirmative() -> String {
    "Yes".into()
}
fn default_negative() -> String {
    "No".into()
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WizardOption {
    pub value: String,
    pub label: String,
}

/// Sent by Rust as `wizard.submit` when the user completes the active form.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WizardSubmit {
    pub id: u64,
    pub fields: serde_json::Value, // map<key, FieldValue>
    #[serde(default)]
    pub cancelled: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DoctorState {}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DoctorSnapshot {
    #[serde(default)]
    pub running: bool,
    #[serde(default)]
    pub checks: Vec<DoctorCheckView>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DoctorCheckView {
    pub id: String,
    /// "ok" | "warn" | "error" | "skipped"
    pub severity: String,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct MonitorState {
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub log_path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct MonitorSnapshot {
    #[serde(default)]
    pub status: DashboardStatus,
    #[serde(default)]
    pub events: Vec<MonitorEvent>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct MonitorEvent {
    #[serde(default)]
    pub time: String, // HH:MM:SS
    #[serde(default)]
    pub iteration: u32,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub tool: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ReposState {
    #[serde(default)]
    pub memory_dir: String,
}

/// Full snapshot of the Repo Intelligence dashboard. Sent as `repos.snapshot`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ReposSnapshot {
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub entries: Vec<RepoEntry>,
    #[serde(default)]
    pub selected: usize,
    #[serde(default)]
    pub memory: Option<RepoMemoryView>,
    #[serde(default)]
    pub scan: Option<ScanResultView>,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub users: Vec<RepoUserView>,
    #[serde(default)]
    pub graph: Option<CallGraphView>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct RepoEntry {
    pub id: String,
    #[serde(default)]
    pub full_name: String,
    #[serde(default)]
    pub platform: String,
    #[serde(default)]
    pub language: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub index_status: String,
    #[serde(default)]
    pub scan_status: String,
    #[serde(default)]
    pub risk_level: String,
    #[serde(default)]
    pub indexed_at: String, // RFC3339 trimmed to date+time
    #[serde(default)]
    pub head_sha: String,
    #[serde(default)]
    pub tree_truncated: bool,
    #[serde(default)]
    pub users_count: u32,
    #[serde(default)]
    pub progress: Option<ProgressInfo>,
    #[serde(default)]
    pub index_error: String,
    #[serde(default)]
    pub scan_error: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ProgressInfo {
    /// "indexing" | "scanning" | "done" | "error"
    pub kind: String,
    #[serde(default)]
    pub message: String,
    /// 0–100, or -1 if unknown
    #[serde(default)]
    pub pct: i32,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct RepoMemoryView {
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub architecture: String,
    #[serde(default)]
    pub primary_lang: String,
    #[serde(default)]
    pub languages: Vec<String>,
    #[serde(default)]
    pub key_files: Vec<String>,
    #[serde(default)]
    pub conventions: Vec<NameValue>,
    #[serde(default)]
    pub dependencies: Vec<NameValue>,
    #[serde(default)]
    pub test_patterns: String,
    #[serde(default)]
    pub ci_summary: String,
    #[serde(default)]
    pub review_hints: String,
    #[serde(default)]
    pub common_issues: Vec<String>,
    #[serde(default)]
    pub user_context: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct NameValue {
    pub name: String,
    #[serde(default)]
    pub value: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ScanResultView {
    #[serde(default)]
    pub scanned_at: String,
    #[serde(default)]
    pub risk_level: String,
    #[serde(default)]
    pub summary: String,
    #[serde(default)]
    pub cves: Vec<Finding>,
    #[serde(default)]
    pub bottlenecks: Vec<Finding>,
    #[serde(default)]
    pub suggestions: Vec<Finding>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct Finding {
    #[serde(default)]
    pub severity: String,
    #[serde(default)]
    pub location: String,
    #[serde(default)]
    pub package: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub fix: String,
    #[serde(default)]
    pub cve_ids: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct RepoUserView {
    #[serde(default)]
    pub login: String,
    #[serde(default)]
    pub role: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CallGraphView {
    #[serde(default)]
    pub node_count: usize,
    #[serde(default)]
    pub edge_count: usize,
    #[serde(default)]
    pub selected: Option<CallNodeView>,
    /// 0-based index of `selected` within `nodes` (when nodes is non-empty).
    #[serde(default)]
    pub selected_idx: usize,
    /// Sample of nodes (capped server-side at ~200) for left-pane navigation.
    #[serde(default)]
    pub nodes: Vec<CallNodeView>,
    #[serde(default)]
    pub callees: Vec<CallNodeView>,
    #[serde(default)]
    pub callers: Vec<CallNodeView>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CallNodeView {
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub file: String,
    #[serde(default)]
    pub line: i32,
    #[serde(default)]
    pub package: String,
}

/// Sent by Rust when user submits the edit form on Memory tab.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RepoEditSubmit {
    pub architecture: String,
    pub review_hints: String,
    pub user_context: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DashboardState {
    #[serde(default)]
    pub context_label: String,
    /// When true, Esc closes the dashboard without quitting the host process.
    #[serde(default)]
    pub overlay: bool,
}

/// Periodic dashboard refresh. Sent by Go as `dashboard.snapshot`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DashboardSnapshot {
    #[serde(default)]
    pub status: DashboardStatus,
    // `default, deserialize_with = "null_as_empty_vec"` accepts both a missing
    // field AND a JSON `null`. Plain `#[serde(default)]` only handles the
    // missing case; Go marshals `nil` slices as `null`, which would reject
    // the whole snapshot and silently fall back to the all-empty default
    // (the symptom we hit in v1.0.36).
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub config: Vec<KeyValue>,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub limits: Vec<KeyValue>,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub usage: Vec<KeyValue>,
    #[serde(default)]
    pub usage_empty_hint: String,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub agents: Vec<AgentInfo>,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub logs: Vec<LogEntry>,
    #[serde(default)]
    pub log_source_path: String,
}

/// Accept either a JSON array OR `null` (Go's encoding of a nil slice) and
/// produce an empty `Vec<T>` in the second case. Used for snapshot fields
/// that Go may emit as `null` when the underlying value is a nil slice.
fn null_as_empty_vec<'de, D, T>(d: D) -> Result<Vec<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: serde::Deserialize<'de>,
{
    Ok(Option::<Vec<T>>::deserialize(d)?.unwrap_or_default())
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DashboardStatus {
    #[serde(default)]
    pub alive: bool,
    #[serde(default)]
    pub pid: i32,
    #[serde(default)]
    pub etime: String,
    #[serde(default)]
    pub cpu_percent: f32,
    #[serde(default)]
    pub rss_mb: f32,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub skill_summary: String,
    #[serde(default, deserialize_with = "null_as_empty_vec")]
    pub channels: Vec<String>,
    #[serde(default)]
    pub plano: ToggleInfo,
    #[serde(default)]
    pub mcp: ToggleInfo,
    #[serde(default)]
    pub gateway_base: String,
    #[serde(default)]
    pub gateway_bind: String,
    #[serde(default)]
    pub run_trace_file: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ToggleInfo {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub detail: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct KeyValue {
    pub k: String,
    pub v: String,
    /// When set, pressing `e` on this row opens an inline editor; submitting
    /// the new value ships a `dashboard.edit` request to Go with this
    /// `yaml_path` (e.g. "agent.max_iterations") so it can patch the on-disk
    /// opsintelligence.yaml via mergeOnboardYAML.
    #[serde(default)]
    pub yaml_path: String,
    /// Optional select choices. When non-empty the editor presents these as
    /// a vertical list instead of a free-form text input. Useful for booleans
    /// and enumerations like `routing.default` / `planning.mode`.
    #[serde(default)]
    pub choices: Vec<String>,
    /// Optional short hint shown under the editor (e.g. "integer ≥ 1").
    #[serde(default)]
    pub hint: String,
}

/// Sent by Go back to Rust after a `dashboard.edit` request. `ok=true` means
/// the YAML was patched on disk; `error` carries a one-line reason on failure
/// (parse error, validation failure, write error). The Rust side shows the
/// message as a transient toast at the bottom of the dashboard.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct DashboardEditResult {
    #[serde(default)]
    pub ok: bool,
    #[serde(default)]
    pub yaml_path: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct AgentInfo {
    pub id: String,
    pub name: String,
    pub status: String, // "running", "pending", "completed", "failed", "cancelled"
    #[serde(default)]
    pub elapsed: String,
    #[serde(default)]
    pub last_phase: String,
    #[serde(default)]
    pub last_message: String,
    #[serde(default)]
    pub error: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct LogEntry {
    #[serde(default)]
    pub time: String, // HH:MM:SS
    #[serde(default)]
    pub source: String,
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub detail: String,
    #[serde(default)]
    pub error: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ReplViewState {
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub model_name: String,
    #[serde(default)]
    pub provider_count: u32,
    #[serde(default)]
    pub skill_count: u32,
    #[serde(default)]
    pub banner: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum AgentDelta {
    Token { text: String },
    ToolCall { name: String, input: String },
    ToolResult { name: String, result: String },
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct AgentEnd {
    #[serde(default)]
    pub iterations: u32,
    #[serde(default)]
    pub usage: TokenUsage,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentError {
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommandSubmit {
    pub text: String,
}
