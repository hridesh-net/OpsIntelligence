//! Repo Intelligence view — port of `cmd/opsintelligence/tui/repos_tui.go`.

use crate::protocol::{
    CallGraphView, CallNodeView, Finding, RepoEntry, RepoMemoryView, ReposSnapshot, ReposState,
    ScanResultView,
};
use crate::theme;
use crate::widgets::textarea::{TextArea, TextAreaView};
use crossterm::event::{KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

const TABS: &[&str] = &["Repos", "Memory", "Scans", "Users", "Graph"];

pub enum ReposOutbound {
    None,
    /// User selected a different repo (by index into the snapshot's entries).
    Select(usize),
    /// User pressed `s` — queue a re-sync for the given repo id.
    Sync(String),
    /// User pressed `r` — force a refresh of the snapshot.
    Refresh,
    /// User submitted the Memory edit form.
    EditSubmit {
        architecture: String,
        review_hints: String,
        user_context: String,
    },
    /// Quit the program.
    Quit,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    Browse,
    Edit,
}

pub struct ReposView {
    pub state: ReposState,
    pub snap: ReposSnapshot,
    active_tab: usize,
    selected: usize,
    scroll: u16,
    mode: Mode,
    edit_field: usize, // 0 = architecture, 1 = review_hints, 2 = user_context
    edit_fields: [TextArea; 3],
    #[allow(dead_code)]
    last_error: String,
}

impl ReposView {
    pub fn new(state: ReposState) -> Self {
        Self {
            state,
            snap: ReposSnapshot::default(),
            active_tab: 0,
            selected: 0,
            scroll: 0,
            mode: Mode::Browse,
            edit_field: 0,
            edit_fields: [
                TextArea::new("Architecture overview…"),
                TextArea::new("Review focus / hints…"),
                TextArea::new("Operator notes…"),
            ],
            last_error: String::new(),
        }
    }

    pub fn apply_snapshot(&mut self, snap: ReposSnapshot) {
        // Preserve local selection if still valid.
        if !snap.entries.is_empty() {
            self.selected = self.selected.min(snap.entries.len().saturating_sub(1));
        } else {
            self.selected = 0;
        }
        self.snap = snap;
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> ReposOutbound {
        if key.kind != KeyEventKind::Press {
            return ReposOutbound::None;
        }
        if self.mode == Mode::Edit {
            return self.handle_edit_key(key);
        }

        match key.code {
            KeyCode::Esc => return ReposOutbound::Quit,
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return ReposOutbound::Quit;
            }
            KeyCode::Char('q') | KeyCode::Char('Q') => return ReposOutbound::Quit,
            KeyCode::Left | KeyCode::BackTab => {
                self.active_tab = (self.active_tab + TABS.len() - 1) % TABS.len();
                self.scroll = 0;
            }
            KeyCode::Right | KeyCode::Tab => {
                self.active_tab = (self.active_tab + 1) % TABS.len();
                self.scroll = 0;
            }
            KeyCode::Up => {
                if self.active_tab == 0 {
                    if self.selected > 0 {
                        self.selected -= 1;
                        return ReposOutbound::Select(self.selected);
                    }
                } else {
                    self.scroll = self.scroll.saturating_sub(1);
                }
            }
            KeyCode::Down => {
                if self.active_tab == 0 {
                    if self.selected + 1 < self.snap.entries.len() {
                        self.selected += 1;
                        return ReposOutbound::Select(self.selected);
                    }
                } else {
                    self.scroll = self.scroll.saturating_add(1);
                }
            }
            KeyCode::PageUp => self.scroll = self.scroll.saturating_sub(10),
            KeyCode::PageDown => self.scroll = self.scroll.saturating_add(10),
            KeyCode::Char('s') | KeyCode::Char('S') => {
                if let Some(entry) = self.snap.entries.get(self.selected) {
                    return ReposOutbound::Sync(entry.id.clone());
                }
            }
            KeyCode::Char('r') | KeyCode::Char('R') => return ReposOutbound::Refresh,
            KeyCode::Char('e') | KeyCode::Char('E') => {
                if self.active_tab == 1 {
                    self.enter_edit_mode();
                }
            }
            _ => {}
        }
        ReposOutbound::None
    }

    fn enter_edit_mode(&mut self) {
        let mem = self.snap.memory.clone().unwrap_or_default();
        self.edit_fields[0].set_value(mem.architecture);
        self.edit_fields[1].set_value(mem.review_hints);
        self.edit_fields[2].set_value(mem.user_context);
        self.edit_field = 0;
        self.mode = Mode::Edit;
    }

    fn handle_edit_key(&mut self, key: KeyEvent) -> ReposOutbound {
        match key.code {
            KeyCode::Esc => {
                self.mode = Mode::Browse;
                return ReposOutbound::None;
            }
            KeyCode::Tab => {
                self.edit_field = (self.edit_field + 1) % 3;
                return ReposOutbound::None;
            }
            KeyCode::BackTab => {
                self.edit_field = (self.edit_field + 2) % 3;
                return ReposOutbound::None;
            }
            KeyCode::Char('s') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                let architecture = self.edit_fields[0].value().to_string();
                let review_hints = self.edit_fields[1].value().to_string();
                let user_context = self.edit_fields[2].value().to_string();
                self.mode = Mode::Browse;
                return ReposOutbound::EditSubmit {
                    architecture,
                    review_hints,
                    user_context,
                };
            }
            _ => {}
        }
        let _ = self.edit_fields[self.edit_field].handle_key(key);
        ReposOutbound::None
    }

    pub fn render(&mut self, f: &mut Frame, area: Rect) {
        if self.mode == Mode::Edit {
            self.render_edit_form(f, area);
            return;
        }

        // Outer frame.
        let inner = crate::widgets::chrome::outer_block(
            f,
            area,
            "OpsIntelligence",
            "Repo Intelligence",
            None,
        );
        // Status pill: total repo count.
        let pill_text = format!(" {} repos ", self.snap.entries.len());
        crate::widgets::chrome::render_pill(f, area, &pill_text, theme::PRIMARY);

        let layout = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1), // tab row
                Constraint::Min(3),    // body panel
                Constraint::Length(1), // command bar
            ])
            .split(inner);

        // Tab row.
        let mut tab_spans: Vec<Span> = vec![Span::raw(" ")];
        for (i, t) in TABS.iter().enumerate() {
            if i > 0 {
                tab_spans.push(Span::styled(" │ ", theme::muted()));
            }
            if i == self.active_tab {
                tab_spans.push(Span::styled(
                    format!(" {} ", t),
                    Style::default()
                        .bg(theme::PRIMARY)
                        .fg(theme::BACKGROUND)
                        .add_modifier(Modifier::BOLD),
                ));
            } else {
                tab_spans.push(Span::styled(format!(" {} ", t), theme::muted()));
            }
        }
        f.render_widget(Paragraph::new(Line::from(tab_spans)), layout[0]);

        // Body panel — titled with active tab + selected repo name.
        let mut title = TABS[self.active_tab.min(TABS.len() - 1)].to_string();
        if let Some(entry) = self.snap.entries.get(self.selected) {
            if !entry.full_name.is_empty() {
                title = format!("{}  ·  {}", title, entry.full_name);
            }
        }
        let body_block = crate::widgets::chrome::panel_block(&title, theme::PRIMARY, true);
        let body_inner = body_block.inner(layout[1]);
        f.render_widget(body_block, layout[1]);

        let lines = match self.active_tab {
            0 => render_repos_tab(&self.snap.entries, self.selected),
            1 => render_memory_tab(&self.snap.memory),
            2 => render_scans_tab(&self.snap.scan),
            3 => render_users_tab(&self.snap.users),
            4 => render_graph_tab(&self.snap.graph),
            _ => Vec::new(),
        };
        let max_scroll = (lines.len() as u16).saturating_sub(body_inner.height);
        if self.scroll > max_scroll {
            self.scroll = max_scroll;
        }
        f.render_widget(
            Paragraph::new(lines)
                .wrap(Wrap { trim: false })
                .scroll((self.scroll, 0)),
            body_inner,
        );

        // Command bar.
        let entries: &[(&str, &str)] = if self.active_tab == 0 {
            &[
                ("↑↓", "Select"),
                ("S", "Sync"),
                ("R", "Refresh"),
                ("⇥", "Tab"),
                ("⎋", "Quit"),
            ]
        } else if self.active_tab == 1 {
            &[
                ("E", "Edit"),
                ("↑↓", "Scroll"),
                ("⇥", "Tab"),
                ("⎋", "Quit"),
            ]
        } else {
            &[("↑↓", "Scroll"), ("⇥", "Tab"), ("⎋", "Quit")]
        };
        crate::widgets::chrome::render_command_bar(f, layout[2], entries);
    }

    fn render_edit_form(&self, f: &mut Frame, area: Rect) {
        let block = Block::default()
            .borders(Borders::ALL)
            .title(" Edit operator memory ")
            .border_style(Style::default().fg(theme::PRIMARY));
        let inner = block.inner(area);
        f.render_widget(block, area);

        let labels = ["Architecture", "Review hints", "Operator notes"];
        let rows = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(5),
                Constraint::Length(5),
                Constraint::Min(3),
                Constraint::Length(1),
            ])
            .split(inner);

        for i in 0..3 {
            let focused = i == self.edit_field;
            let label_style = if focused {
                theme::primary()
            } else {
                theme::muted()
            };
            let block = Block::default()
                .borders(Borders::ALL)
                .title(format!(" {} ", labels[i]))
                .title_style(label_style)
                .border_style(if focused {
                    Style::default().fg(theme::PRIMARY)
                } else {
                    Style::default().fg(theme::BORDER)
                });
            let area = rows[i];
            let inner = block.inner(area);
            f.render_widget(block, area);
            let ta = TextAreaView {
                area: &self.edit_fields[i],
                style: Style::default().fg(theme::ON_SURFACE),
                placeholder_style: theme::muted(),
                focused,
            };
            f.render_widget(ta, inner);
        }
        f.render_widget(
            Paragraph::new(Span::styled(
                "Tab next · Shift+Tab prev · Ctrl+S save · Esc cancel",
                theme::muted(),
            )),
            rows[3],
        );
    }
}

// ── Tab renderers ──────────────────────────────────────────────────────────

fn render_repos_tab(entries: &[RepoEntry], selected: usize) -> Vec<Line<'static>> {
    let mut lines: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Repositories",
        theme::header(),
    ))];
    lines.push(Line::raw(""));
    if entries.is_empty() {
        lines.push(Line::from(Span::styled(
            "No repos configured. Use `opsintelligence repos add …`.",
            theme::muted(),
        )));
        return lines;
    }
    lines.push(Line::from(Span::styled(
        format!(
            "{:<2} {:<32} {:<8} {:<10} {:<10} {}",
            "", "NAME", "LANG", "INDEX", "SCAN", "RISK"
        ),
        theme::muted(),
    )));
    lines.push(Line::from(Span::styled("─".repeat(74), theme::muted())));
    for (i, e) in entries.iter().enumerate() {
        let marker = if i == selected { "›" } else { " " };
        let name_style = if i == selected {
            Style::default()
                .fg(theme::PRIMARY)
                .add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(theme::ON_SURFACE)
        };
        let (idx_text, idx_style) = colorize_status(&e.index_status);
        let (scan_text, scan_style) = colorize_status(&e.scan_status);
        let risk_style = risk_style(&e.risk_level);

        let mut row: Vec<Span> = vec![
            Span::styled(format!("{} ", marker), Style::default().fg(theme::PRIMARY)),
            Span::styled(format!("{:<32}", truncate(&e.full_name, 32)), name_style),
            Span::raw(" "),
            Span::styled(
                format!("{:<8}", truncate(&e.language, 8)),
                theme::muted(),
            ),
            Span::raw(" "),
            Span::styled(format!("{:<10}", idx_text), idx_style),
            Span::raw(" "),
            Span::styled(format!("{:<10}", scan_text), scan_style),
            Span::raw(" "),
            Span::styled(e.risk_level.clone(), risk_style),
        ];
        if e.tree_truncated {
            row.push(Span::raw("  "));
            row.push(Span::styled("⚠ tree-truncated", theme::error_style()));
        }
        lines.push(Line::from(row));

        if let Some(p) = &e.progress {
            let pct = if p.pct >= 0 {
                format!(" {}%", p.pct)
            } else {
                String::new()
            };
            let bar = progress_bar(p.pct, 20);
            lines.push(Line::from(vec![
                Span::raw("   "),
                Span::styled(format!("[{}] ", p.kind), theme::muted()),
                Span::styled(bar, Style::default().fg(theme::PRIMARY)),
                Span::styled(pct, theme::muted()),
                Span::raw("  "),
                Span::styled(truncate(&p.message, 60), theme::muted()),
            ]));
        }
        if !e.index_error.is_empty() {
            lines.push(Line::from(vec![
                Span::raw("   "),
                Span::styled("index error: ", theme::error_style()),
                Span::styled(truncate(&e.index_error, 80), theme::error_style()),
            ]));
        }
        if !e.scan_error.is_empty() {
            lines.push(Line::from(vec![
                Span::raw("   "),
                Span::styled("scan error: ", theme::error_style()),
                Span::styled(truncate(&e.scan_error, 80), theme::error_style()),
            ]));
        }
    }

    // Detail block for selected repo.
    if let Some(e) = entries.get(selected) {
        lines.push(Line::raw(""));
        lines.push(Line::from(Span::styled("Selected", theme::header())));
        push_kv(&mut lines, "id", &e.id);
        push_kv(&mut lines, "description", &nz(&e.description));
        push_kv(&mut lines, "head_sha", &nz(&e.head_sha));
        push_kv(&mut lines, "indexed_at", &nz(&e.indexed_at));
        push_kv(&mut lines, "users", &e.users_count.to_string());
    }
    lines
}

fn render_memory_tab(mem: &Option<RepoMemoryView>) -> Vec<Line<'static>> {
    let Some(m) = mem else {
        return vec![Line::from(Span::styled(
            "No memory file yet for the selected repo. Run `opsintelligence repos index <id>`.",
            theme::muted(),
        ))];
    };
    let mut out: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Repo Memory",
        theme::header(),
    ))];
    out.push(Line::raw(""));
    push_kv(&mut out, "updated_at", &nz(&m.updated_at));
    push_kv(&mut out, "primary_lang", &nz(&m.primary_lang));
    if !m.languages.is_empty() {
        push_kv(&mut out, "languages", &m.languages.join(", "));
    }
    out.push(Line::raw(""));
    if !m.architecture.is_empty() {
        out.push(Line::from(Span::styled("Architecture", theme::neon())));
        for line in wrap_paragraph(&m.architecture, 90) {
            out.push(Line::raw(line));
        }
        out.push(Line::raw(""));
    }
    if !m.key_files.is_empty() {
        out.push(Line::from(Span::styled("Key files", theme::neon())));
        for f in &m.key_files {
            out.push(Line::from(vec![
                Span::styled("  · ", theme::muted()),
                Span::raw(f.clone()),
            ]));
        }
        out.push(Line::raw(""));
    }
    if !m.conventions.is_empty() {
        out.push(Line::from(Span::styled("Conventions", theme::neon())));
        for c in &m.conventions {
            out.push(Line::from(vec![
                Span::styled("  · ", theme::muted()),
                Span::styled(
                    c.name.clone(),
                    Style::default().add_modifier(Modifier::BOLD),
                ),
                Span::raw(": "),
                Span::styled(c.value.clone(), theme::muted()),
            ]));
        }
        out.push(Line::raw(""));
    }
    if !m.dependencies.is_empty() {
        out.push(Line::from(Span::styled("Dependencies", theme::neon())));
        for d in &m.dependencies {
            out.push(Line::from(vec![
                Span::styled("  · ", theme::muted()),
                Span::raw(d.name.clone()),
                Span::raw(" "),
                Span::styled(d.value.clone(), theme::muted()),
            ]));
        }
        out.push(Line::raw(""));
    }
    if !m.review_hints.is_empty() {
        out.push(Line::from(Span::styled("Review hints", theme::neon())));
        for line in wrap_paragraph(&m.review_hints, 90) {
            out.push(Line::raw(line));
        }
        out.push(Line::raw(""));
    }
    if !m.common_issues.is_empty() {
        out.push(Line::from(Span::styled("Common issues", theme::neon())));
        for s in &m.common_issues {
            out.push(Line::from(vec![
                Span::styled("  · ", theme::muted()),
                Span::raw(s.clone()),
            ]));
        }
        out.push(Line::raw(""));
    }
    if !m.test_patterns.is_empty() {
        out.push(Line::from(Span::styled("Test patterns", theme::neon())));
        for line in wrap_paragraph(&m.test_patterns, 90) {
            out.push(Line::raw(line));
        }
        out.push(Line::raw(""));
    }
    if !m.ci_summary.is_empty() {
        out.push(Line::from(Span::styled("CI summary", theme::neon())));
        for line in wrap_paragraph(&m.ci_summary, 90) {
            out.push(Line::raw(line));
        }
        out.push(Line::raw(""));
    }
    if !m.user_context.is_empty() {
        out.push(Line::from(Span::styled("Operator notes", theme::neon())));
        for line in wrap_paragraph(&m.user_context, 90) {
            out.push(Line::raw(line));
        }
    }
    out
}

fn render_scans_tab(scan: &Option<ScanResultView>) -> Vec<Line<'static>> {
    let Some(s) = scan else {
        return vec![Line::from(Span::styled(
            "No scan result for the selected repo. Run `opsintelligence repos scan <id>`.",
            theme::muted(),
        ))];
    };
    let mut out: Vec<Line<'static>> = vec![
        Line::from(vec![
            Span::styled("Scan ", theme::header()),
            Span::styled(s.risk_level.clone(), risk_style(&s.risk_level)),
        ]),
        Line::raw(""),
    ];
    push_kv(&mut out, "scanned_at", &nz(&s.scanned_at));
    if !s.summary.is_empty() {
        out.push(Line::raw(""));
        for line in wrap_paragraph(&s.summary, 90) {
            out.push(Line::raw(line));
        }
    }
    out.push(Line::raw(""));
    render_findings(&mut out, "CVEs", &s.cves, true);
    render_findings(&mut out, "Bottlenecks", &s.bottlenecks, false);
    render_findings(&mut out, "Suggestions", &s.suggestions, false);
    out
}

fn render_findings(out: &mut Vec<Line<'static>>, title: &str, items: &[Finding], cve: bool) {
    if items.is_empty() {
        return;
    }
    out.push(Line::from(Span::styled(title.to_string(), theme::neon())));
    for f in items {
        let sev_style = risk_style(&f.severity);
        let mut row = vec![
            Span::styled(
                format!(" [{}] ", f.severity.to_uppercase()),
                sev_style,
            ),
        ];
        if cve {
            if !f.package.is_empty() {
                row.push(Span::styled(
                    f.package.clone(),
                    Style::default().add_modifier(Modifier::BOLD),
                ));
                if !f.version.is_empty() {
                    row.push(Span::styled(format!("@{}", f.version), theme::muted()));
                }
                row.push(Span::raw("  "));
            }
        } else if !f.location.is_empty() {
            row.push(Span::styled(
                f.location.clone(),
                Style::default().add_modifier(Modifier::BOLD),
            ));
            row.push(Span::raw("  "));
        }
        row.push(Span::raw(truncate(&f.description, 100)));
        out.push(Line::from(row));
        if !f.fix.is_empty() {
            out.push(Line::from(vec![
                Span::raw("     "),
                Span::styled("fix: ", theme::muted()),
                Span::styled(truncate(&f.fix, 100), theme::muted()),
            ]));
        }
        if !f.cve_ids.is_empty() {
            out.push(Line::from(vec![
                Span::raw("     "),
                Span::styled(f.cve_ids.join(", "), theme::muted()),
            ]));
        }
    }
    out.push(Line::raw(""));
}

fn render_users_tab(users: &[crate::protocol::RepoUserView]) -> Vec<Line<'static>> {
    let mut out: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Users",
        theme::header(),
    ))];
    out.push(Line::raw(""));
    if users.is_empty() {
        out.push(Line::from(Span::styled(
            "No users mapped for this repo yet.",
            theme::muted(),
        )));
        return out;
    }
    for u in users {
        out.push(Line::from(vec![
            Span::styled("  · ", theme::muted()),
            Span::styled(
                u.login.clone(),
                Style::default().add_modifier(Modifier::BOLD),
            ),
            Span::raw("  "),
            Span::styled(u.role.clone(), theme::muted()),
        ]));
    }
    out
}

fn render_graph_tab(graph: &Option<CallGraphView>) -> Vec<Line<'static>> {
    let Some(g) = graph else {
        return vec![Line::from(Span::styled(
            "No call graph for the selected repo.",
            theme::muted(),
        ))];
    };
    let mut out: Vec<Line<'static>> = vec![Line::from(Span::styled(
        "Call Graph",
        theme::header(),
    ))];
    out.push(Line::from(vec![
        Span::styled(
            format!("{} nodes  ", g.node_count),
            theme::muted(),
        ),
        Span::styled(format!("{} edges", g.edge_count), theme::muted()),
    ]));
    out.push(Line::raw(""));

    if let Some(s) = &g.selected {
        out.push(Line::from(Span::styled(
            format!("● {}  ({})", s.name, s.kind),
            theme::primary(),
        )));
        out.push(Line::from(vec![
            Span::styled("   ", theme::muted()),
            Span::styled(
                format!("{}:{}", s.file, s.line),
                theme::muted(),
            ),
        ]));
        if !s.package.is_empty() {
            out.push(Line::from(vec![
                Span::styled("   pkg ", theme::muted()),
                Span::styled(s.package.clone(), theme::muted()),
            ]));
        }
        out.push(Line::raw(""));
        out.push(Line::from(Span::styled(
            format!("Callees ({})", g.callees.len()),
            theme::neon(),
        )));
        for n in &g.callees {
            out.push(call_node_line(n));
        }
        if g.callees.is_empty() {
            out.push(Line::from(Span::styled("  (none)", theme::muted())));
        }
        out.push(Line::raw(""));
        out.push(Line::from(Span::styled(
            format!("Callers ({})", g.callers.len()),
            theme::neon(),
        )));
        for n in &g.callers {
            out.push(call_node_line(n));
        }
        if g.callers.is_empty() {
            out.push(Line::from(Span::styled("  (none)", theme::muted())));
        }
    } else {
        out.push(Line::from(Span::styled(
            "No node selected.",
            theme::muted(),
        )));
    }
    out
}

fn call_node_line(n: &CallNodeView) -> Line<'static> {
    Line::from(vec![
        Span::styled("  ↳ ", theme::muted()),
        Span::styled(
            n.name.clone(),
            Style::default().add_modifier(Modifier::BOLD),
        ),
        Span::raw("  "),
        Span::styled(format!("({})", n.kind), theme::muted()),
        Span::raw("  "),
        Span::styled(format!("{}:{}", n.file, n.line), theme::muted()),
    ])
}

// ── Helpers ────────────────────────────────────────────────────────────────

fn push_kv(out: &mut Vec<Line<'static>>, k: &str, v: &str) {
    const KEY_W: usize = 14;
    let pad = if k.len() < KEY_W {
        " ".repeat(KEY_W - k.len())
    } else {
        " ".to_string()
    };
    out.push(Line::from(vec![
        Span::styled(format!("  {}{}", k, pad), theme::muted()),
        Span::styled(v.to_string(), Style::default().fg(theme::ON_SURFACE)),
    ]));
}

fn progress_bar(pct: i32, width: usize) -> String {
    let mut bar = String::with_capacity(width);
    let filled = if pct < 0 {
        0
    } else {
        ((pct.max(0).min(100) as usize) * width) / 100
    };
    for i in 0..width {
        bar.push(if i < filled { '█' } else { '░' });
    }
    bar
}

fn colorize_status(s: &str) -> (String, Style) {
    let s = s.to_ascii_lowercase();
    let style = match s.as_str() {
        "pending" | "queued" => Style::default().fg(theme::NEON),
        "indexing" | "scanning" | "running" => theme::primary(),
        "indexed" | "scanned" | "done" => Style::default().fg(theme::SUCCESS),
        "failed" | "error" => theme::error_style(),
        _ => theme::muted(),
    };
    (s, style)
}

fn risk_style(level: &str) -> Style {
    match level.to_ascii_lowercase().as_str() {
        "critical" | "high" => theme::error_style(),
        "medium" => Style::default().fg(theme::WARN),
        "low" => theme::muted(),
        "info" => theme::muted(),
        _ => theme::muted(),
    }
}

fn truncate(s: &str, max: usize) -> String {
    let chars: Vec<char> = s.chars().collect();
    if chars.len() <= max {
        return s.to_string();
    }
    let mut t: String = chars.into_iter().take(max.saturating_sub(1)).collect();
    t.push('…');
    t
}

fn nz(s: &str) -> String {
    let t = s.trim();
    if t.is_empty() {
        "—".to_string()
    } else {
        t.to_string()
    }
}

fn wrap_paragraph(text: &str, width: usize) -> Vec<String> {
    let mut out = Vec::new();
    for paragraph in text.split('\n') {
        if paragraph.trim().is_empty() {
            out.push(String::new());
            continue;
        }
        let mut cur = String::new();
        for word in paragraph.split_whitespace() {
            if cur.is_empty() {
                cur.push_str(word);
            } else if cur.chars().count() + 1 + word.chars().count() <= width {
                cur.push(' ');
                cur.push_str(word);
            } else {
                out.push(cur);
                cur = word.to_string();
            }
        }
        if !cur.is_empty() {
            out.push(cur);
        }
    }
    out
}
