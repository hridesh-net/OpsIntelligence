# SonarQube policy — example team

- **Quality gate**: `new_coverage ≥ 80%`, `new_duplicated_lines_density ≤ 3%`, `new_violations = 0`.
- **Severity thresholds**:
  - BLOCKER → treat as a merge blocker, page on-call if discovered on `main`.
  - CRITICAL → must be fixed in the same PR that introduces it, or an opened ticket linked from the PR.
  - MAJOR → fix in the same sprint; the agent should flag them in PR review but not block.
  - MINOR / INFO → ignore unless they are part of a broader cleanup PR.
- **Exception process**: if a finding is a false positive, add the rule+component to the team's WONTFIX list and reference the ticket or discussion in the Sonar comment. Never silence rules globally without security sign-off.
- **New vs overall**: the agent always prefers the **new code** view when commenting on a PR. It may reference overall code only to flag longstanding tech-debt hotspots in `incidents.md`.
