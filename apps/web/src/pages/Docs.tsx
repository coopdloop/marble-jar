import { useMemo, useState } from "react";
import { marked } from "marked";
import DOMPurify from "dompurify";
import { BookOpen } from "lucide-react";
import { Walkthrough } from "@/components/docs/Walkthrough";
import { cn } from "@/lib/utils";

// Single source of truth: the markdown lives in /docs at the repo root and is
// inlined at build time.
import overviewMd from "@docs/index.md?raw";
import gettingStartedMd from "@docs/getting-started.md?raw";
import sendingMarblesMd from "@docs/sending-marbles.md?raw";
import integrationsMd from "@docs/integrations.md?raw";
import configurationMd from "@docs/configuration.md?raw";
import apiMd from "@docs/api.md?raw";

marked.use({ gfm: true });

interface DocSection {
  id: string;
  label: string;
  md?: string;
}

const SECTIONS: DocSection[] = [
  { id: "tour", label: "Guided tour" },
  { id: "overview", label: "Overview", md: overviewMd },
  { id: "getting-started", label: "Getting started", md: gettingStartedMd },
  { id: "sending-marbles", label: "Sending marbles", md: sendingMarblesMd },
  { id: "integrations", label: "Integrations & OBO", md: integrationsMd },
  { id: "configuration", label: "Configuration", md: configurationMd },
  { id: "api", label: "API reference", md: apiMd },
];

/** Relative .md links (./getting-started.md) become in-app section links. */
function renderMarkdown(md: string): string {
  const linked = md.replace(/\]\(\.\/([a-z-]+)\.md\)/g, "](#doc-$1)");
  const html = marked.parse(linked, { async: false });
  return DOMPurify.sanitize(html);
}

export function DocsPage() {
  const [activeId, setActiveId] = useState("tour");
  const active = SECTIONS.find((s) => s.id === activeId) ?? SECTIONS[0]!;

  const html = useMemo(() => (active.md ? renderMarkdown(active.md) : null), [active]);

  const onContentClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const anchor = (e.target as HTMLElement).closest("a");
    const hash = anchor?.getAttribute("href") ?? "";
    if (hash.startsWith("#doc-")) {
      e.preventDefault();
      setActiveId(hash.slice(5));
    }
  };

  return (
    <div className="space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold tracking-tight">
          <BookOpen className="h-4 w-4 text-primary" />
          Docs
        </h1>
        <p className="text-xs text-muted-foreground">
          How Marble Jar works — rendered from the markdown in /docs.
        </p>
      </div>

      <div className="flex flex-col gap-5 md:flex-row md:items-start">
        <nav className="flex gap-1 overflow-x-auto md:w-44 md:shrink-0 md:flex-col">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              onClick={() => setActiveId(s.id)}
              className={cn(
                "whitespace-nowrap rounded-lg px-3 py-1.5 text-left text-sm transition-colors",
                s.id === activeId
                  ? "bg-primary/10 font-medium text-primary"
                  : "text-muted-foreground hover:bg-secondary hover:text-foreground",
              )}
            >
              {s.label}
            </button>
          ))}
        </nav>

        <div className="min-w-0 flex-1">
          {active.id === "tour" ? (
            <div className="space-y-5">
              <Walkthrough />
              <DocBody html={renderMarkdown(gettingStartedMd)} onClick={onContentClick} />
            </div>
          ) : html ? (
            <DocBody html={html} onClick={onContentClick} />
          ) : null}
        </div>
      </div>
    </div>
  );
}

function DocBody({ html, onClick }: { html: string; onClick: (e: React.MouseEvent<HTMLDivElement>) => void }) {
  return (
    <div
      className="docs-prose glass rounded-2xl p-6"
      onClick={onClick}
      // Content is our own bundled markdown, sanitized with DOMPurify.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
