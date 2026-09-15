/**
 * The starter template library: real files that live in /docs/templates and are
 * inlined at build time, same as the docs markdown. The library page renders a
 * card per entry, and the tests assert every file has metadata here.
 */

export interface TemplateMeta {
  /** Basename inside /docs/templates. */
  file: string;
  label: string;
  /** Where the file goes in the consumer's project. */
  install: string;
  description: string;
  tags: string[];
  scopes: string[];
  language: "markdown" | "bash" | "json" | "yaml";
}

export interface TemplateFile extends TemplateMeta {
  contents: string;
}

const TEMPLATES: TemplateMeta[] = [
  {
    file: "AGENTS.md",
    label: "Agent reporting contract",
    install: "AGENTS.md at the repo root (or your harness' system prompt)",
    description:
      "The rules an agent follows so nothing finishes unreported: when to log, field conventions, idempotency keys, honest estimates, failure etiquette, and a definition-of-done checklist. Every curl in it is executed by the docs smoke test.",
    tags: ["every harness", "copy-paste curls", "no code changes"],
    scopes: ["marbles:write", "rollups:write"],
    language: "markdown",
  },
  {
    file: "CLAUDE.md",
    label: "Claude Code entry point",
    install: "CLAUDE.md at the repo root, next to AGENTS.md",
    description:
      "Imports the contract above instead of duplicating it, and shows the Stop hook that makes reporting automatic rather than remembered.",
    tags: ["Claude Code", "hooks", "one import"],
    scopes: ["marbles:write"],
    language: "markdown",
  },
  {
    file: "marble-jar.sh",
    label: "Shell reporter library",
    install: "scripts/marble-jar.sh, then `source` it",
    description:
      "mj_log, mj_log_failed, mj_rollup, mj_objective_add, mj_dispatch, mj_search, mj_status, mj_streak and mj_export. Defaults project, agent and idempotency keys from git so call sites stay one line.",
    tags: ["bash", "curl + jq", "cursor-paged export"],
    scopes: ["marbles:write", "rollups:write", "objectives:write"],
    language: "bash",
  },
  {
    file: "post-commit.sh",
    label: "Git post-commit hook",
    install: ".git/hooks/post-commit (or hooks/ in-repo + `git config core.hooksPath`)",
    description:
      "One marble per commit, keyed on the commit SHA so amending cannot double-count. Records branch, author, files and line counts, and marks token usage as a diff-size estimate.",
    tags: ["zero effort", "never blocks a commit", "estimated usage"],
    scopes: ["marbles:write"],
    language: "bash",
  },
  {
    file: "marble-jar-ci.yml",
    label: "GitHub Actions reporter",
    install: ".github/workflows/marble-jar-report.yml",
    description:
      "Turns every completed CI run into a marble, attaches a rollup when real usage becomes known, and writes the jar state into the job summary. continue-on-error keeps reporting out of the build path.",
    tags: ["workflow_run trigger", "job summary", "idempotent re-runs"],
    scopes: ["marbles:write", "rollups:write"],
    language: "yaml",
  },
  {
    file: "dispatch-rules.json",
    label: "Dispatch rule presets",
    install: "POST each rule to /v1/rules, or import by hand in the dashboard",
    description:
      "Five starting rules — costly work to Slack, failures to Jira, a project to Teams, security keywords to a signed webhook, and long runs to review. Includes the /v1/rules/test preview command so you can see matches before enabling.",
    tags: ["auto-dispatch", "previewable", "placeholders to fill"],
    scopes: ["rules:write"],
    language: "json",
  },
  {
    file: "weekly-report.sh",
    label: "Weekly team report",
    install: "scripts/weekly-report.sh, run from cron or CI",
    description:
      "Rolls the last N days into markdown: volume, spend, tokens, agent hours, objective budget burn, unassigned marbles, stuck dispatches and a daily activity histogram. Optionally ships it through /v1/objectives/:id/ship.",
    tags: ["reporting", "read-only by default", "markdown out"],
    scopes: ["jar:read", "marbles:read", "objectives:read"],
    language: "bash",
  },
];

const MIME: Record<TemplateMeta["language"], string> = {
  markdown: "text/markdown",
  bash: "text/x-shellscript",
  json: "application/json",
  yaml: "text/yaml",
};

const raw = import.meta.glob("../../../../docs/templates/*.{md,sh,json,yml}", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function basename(path: string): string {
  return path.split("/").pop() ?? path;
}

export const templates: TemplateFile[] = Object.entries(raw)
  .map(([path, contents]) => {
    const file = basename(path);
    const meta = TEMPLATES.find((t) => t.file === file);
    if (!meta) return null;
    return { ...meta, contents };
  })
  .filter((t): t is TemplateFile => t !== null);

/** Files present on disk but missing from the registry above — a test guards this. */
export const unmappedTemplates = Object.keys(raw).map(basename).filter(
  (file) => !TEMPLATES.some((t) => t.file === file),
);

export function downloadName(t: TemplateFile): string {
  return t.file;
}

export function downloadMime(t: TemplateFile): string {
  return MIME[t.language];
}
