# Multi-cloud DevOps tools (`devops.cloud.*`) — IAM and setup

OpsIntelligence exposes **read-only** agent tools:

| Tool | AWS | Azure | GCP |
|------|-----|-------|-----|
| `devops.cloud.inventory` | Resource Groups Tagging API (`tag:GetResources`) | ARM resources list | Cloud Asset `SearchAllResources` |
| `devops.cloud.cost_summary` | Cost Explorer `GetCostAndUsage` | Cost Management query | Billing API project billing info (link only; see below) |
| `devops.cloud.audit_events` | CloudTrail `LookupEvents` | Activity Log (Insights) | Logging read on `cloudaudit.googleapis.com%2Factivity` |

Enable only the features you need (`inventory`, `cost`, `audit` in `devops.cloud.*` YAML). **Billing and audit are separate trust boundaries** from workload read access.

## AWS (example policy skeleton)

Attach a customer-managed policy to the role or user used by the agent (or to the role assumed via `devops.cloud.aws.role_arn`).

**Inventory**

- `tag:GetResources`
- `tag:DescribeReportCreation` (optional; not required for basic listing)

**Cost** (often requires account-level Cost Explorer opt-in)

- `ce:GetCostAndUsage`
- `ce:GetDimensionValues` (optional)

**Audit**

- `cloudtrail:LookupEvents`
- `cloudtrail:DescribeTrails` (optional)

**STS** (when using `role_arn`)

- `sts:AssumeRole` on the caller toward the target role

**Diagnostics**

- `sts:GetCallerIdentity` (used by `devops.diagnose`)

Scope with **resource tags** and **`devops.cloud.aws.regions`** in config to avoid whole-account scans where possible.

## Azure

Use an **app registration** + **client secret** (`devops.cloud.azure.*`).

**Inventory**

- `Reader` on the subscription, or a custom role with `Microsoft.Resources/subscriptions/resources/read`

**Cost Management**

- `Microsoft.CostManagement/query/read` on the subscription (often via **Cost Management Reader**)

**Activity Log**

- `Microsoft.Insights/EventTypes/Events/Read` at subscription scope (e.g. **Monitoring Reader** plus read on activity log)

Prefer **`devops.cloud.azure.resource_group`** or ARM **tag filters** to limit inventory to an application.

## GCP

Use a **service account JSON** via `GOOGLE_APPLICATION_CREDENTIALS` or `devops.cloud.gcp.credentials_path`.

**Cloud Asset Inventory**

- `cloudasset.assets.searchAllResources` (roles such as **Cloud Asset Viewer**)

**Billing info** (`devops.cloud.cost_summary`)

- `resourcemanager.projects.get` and `cloudbilling.projects.getBillingInfo` on the project

Detailed **time-series cost** usually requires **Billing Account** access or **BigQuery billing export**; the tool returns linkage plus a note when full cost series are not available.

**Audit logs**

- `logging.logEntries.list` on the project (e.g. **Logs Viewer** scoped to Admin Activity audit log)

Enable **Cloud Asset API** and **Cloud Logging API** on the project. Filter inventory with **labels** in config or tool arguments.

## Architecture review

After pulling inventory, cost, and audit snippets with the tools above, use team policy Markdown and the skill [`skills/devops/cloud-architecture-review.md`](../skills/devops/cloud-architecture-review.md) to structure LLM-assisted reviews (no automatic remediation).
