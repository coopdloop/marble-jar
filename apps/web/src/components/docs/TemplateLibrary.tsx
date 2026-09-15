import { useState } from "react";
import { Check, ChevronDown, Copy, Download, FileCode2, KeyRound } from "lucide-react";
import { Badge, Button, Card } from "@/components/ui/primitives";
import { copyToClipboard, cn } from "@/lib/utils";
import { downloadTextFile } from "@/lib/export";
import { downloadMime, downloadName, templates, unmappedTemplates } from "@/lib/templates";
import type { TemplateFile } from "@/lib/templates";

/**
 * Copy/download surface for the files in /docs/templates. The content is inlined
 * at build time, so a download never touches the network.
 */
export function TemplateLibrary() {
  const [open, setOpen] = useState<string | null>(templates[0]?.file ?? null);
  const [copied, setCopied] = useState<string | null>(null);

  async function copy(t: TemplateFile) {
    if (!(await copyToClipboard(t.contents))) return;
    setCopied(t.file);
    window.setTimeout(() => setCopied(null), 1800);
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <p className="text-sm">
          Seven starter files, kept in <code>docs/templates/</code> in the repo and
          rendered here verbatim. Take the ones that fit your harness: the contract
          and the CI/hook reporters are what make the jar a project-management and
          reporting tool rather than a log sink.
        </p>
        {unmappedTemplates.length > 0 ? (
          <p className="mt-2 text-xs text-destructive">
            Missing from the library registry: {unmappedTemplates.join(", ")}
          </p>
        ) : null}
      </Card>

      {templates.map((t) => {
        const expanded = open === t.file;
        return (
          <Card key={t.file} className="overflow-hidden">
            <div className="flex flex-wrap items-start justify-between gap-3 p-4">
              <button
                type="button"
                onClick={() => setOpen(expanded ? null : t.file)}
                className="flex min-w-0 flex-1 items-start gap-3 text-left"
              >
                <FileCode2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{t.label}</span>
                    <code className="text-[11px] text-muted-foreground">{t.file}</code>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">{t.description}</p>
                  <p className="mt-1.5 text-[11px] text-muted-foreground">
                    <span className="uppercase tracking-wider">goes in</span> {t.install}
                  </p>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {t.tags.map((tag) => (
                      <Badge key={tag}>{tag}</Badge>
                    ))}
                  </div>
                </div>
              </button>

              <div className="flex shrink-0 items-center gap-2">
                <Button variant="outline" size="sm" onClick={() => void copy(t)}>
                  {copied === t.file ? (
                    <Check className="h-3.5 w-3.5 text-emerald-400" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                  {copied === t.file ? "Copied" : "Copy"}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => downloadTextFile(downloadName(t), t.contents, downloadMime(t))}
                >
                  <Download className="h-3.5 w-3.5" />
                  Download
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={expanded ? "Collapse" : "Preview"}
                  onClick={() => setOpen(expanded ? null : t.file)}
                >
                  <ChevronDown className={cn("h-4 w-4 transition-transform", expanded && "rotate-180")} />
                </Button>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-1.5 border-t border-border/40 px-4 py-2">
              <KeyRound className="h-3 w-3 text-muted-foreground" />
              <span className="text-[11px] uppercase tracking-wider text-muted-foreground">
                key scopes
              </span>
              {t.scopes.map((s) => (
                <code key={s} className="rounded bg-secondary px-1.5 py-0.5 text-[11px]">
                  {s}
                </code>
              ))}
            </div>

            {expanded ? (
              <pre className="max-h-[420px] overflow-auto border-t border-border/40 bg-background/50 p-4 text-[11px] leading-relaxed">
                <code>{t.contents}</code>
              </pre>
            ) : null}
          </Card>
        );
      })}
    </div>
  );
}
