# Marble Jar docs

> Every finished task, a marble in the jar. Every jar, a story you can tell your team.

Marble Jar is the layer between agent observability and team communication.
Agents report each completed unit of work as a **marble** via the SDK, MCP
server, REST API or a webhook. Marbles flow into a live queue where humans
review and dispatch updates — one click or by rule — to Jira, Slack, Teams or a
custom webhook, always **on-behalf-of** a real person so every action is
auditable.

## Core concepts

| Concept | What it is |
| --- | --- |
| **Marble** | One completed unit of agent work: summary, model, project, cost, tokens, duration, trace link. |
| **Jar** | The live queue. New marbles physically drop into it in real time. |
| **Objective** | A bigger jar: groups marbles for cost, token and time rollups, with optional budgets. |
| **Rule** | Declarative condition → dispatch target. Automates the "post an update" step. |
| **Dispatch** | One outbound action (Jira issue, Slack message, Teams post, webhook call) with retries, dead-lettering and replay. |
| **OBO connection** | An on-behalf-of OAuth link so dispatches act *as you*, not as an anonymous bot. |

## The moving parts

| Component | Stack | Port |
| --- | --- | --- |
| `services/core` | Go (Gin) API + migrations | 8080 |
| `services/core` dispatch worker | Go, per-integration goroutine pools | 8081 |
| `services/mcp-bridge` | Python MCP server for agents | 8090 |
| `apps/web` | React UI (the jar lives here) | 5173 |
| `packages/sdk-ts` | TypeScript client, `@marble-jar/client` | — |

## Where to go next

- **New here?** Start with the guided tour (above, in the app) or
  [Getting started](./getting-started.md).
- **Connecting agents:** [Sending marbles](./sending-marbles.md).
- **Starter files for harnesses, hooks and CI:**
  [Templates](./templates.md).
- **Wiring up Jira/Slack/Teams:** [Integrations & OBO](./integrations.md) —
  including `OAUTH_ISSUER_URL` and friends.
- **Every environment variable:** [Configuration](./configuration.md).
- **Raw HTTP surface:** [API reference](./api.md).
