import { describe, expect, test } from "bun:test";
import { markdownToHtml } from "satteri";

import { githubAlerts } from "../src/plugins/github-alerts";

function render(markdown: string): string {
  const result = markdownToHtml(markdown, {
    mdastPlugins: [githubAlerts()],
  });

  if (result instanceof Promise) {
    throw new TypeError("The GitHub Alerts plugin must remain synchronous.");
  }

  return result.html;
}

describe("githubAlerts", () => {
  test.each([
    ["NOTE", "note", "Note", "info"],
    ["TIP", "tip", "Tip", "light-bulb"],
    ["IMPORTANT", "important", "Important", "report"],
    ["WARNING", "warning", "Warning", "alert"],
    ["CAUTION", "caution", "Caution", "stop"],
  ])(
    "renders %s with GitHub-compatible markup",
    (marker: string, slug: string, label: string, icon: string) => {
      const html = render(`> [!${marker}]\n> Alert body.`);

      expect(html).toContain(
        `<div class="markdown-alert markdown-alert-${slug}" dir="auto">`,
      );
      expect(html).toContain(
        `<p class="markdown-alert-title" dir="auto"><svg class="octicon octicon-${icon} mr-2"`,
      );
      expect(html).toContain(`aria-hidden="true"`);
      expect(html).toContain(`</svg>${label}</p>`);
      expect(html).toContain("<p>Alert body.</p>");
      expect(html).not.toContain(`[!${marker}]`);
      expect(html).not.toContain("starlight-aside");
      expect(html).not.toContain("<aside");
    },
  );

  test("preserves rich Markdown in a multi-block body", () => {
    const html = render(`> [!NOTE]\n> **Bold** and [link](https://example.com).\n>\n> - one\n> - two\n>\n> \`inline\`\n>\n> \`\`\`sh\n> echo hello\n> \`\`\``);

    expect(html).toContain("<strong>Bold</strong>");
    expect(html).toContain('<a href="https://example.com">link</a>');
    expect(html).toContain("<ul>");
    expect(html).toContain("<li>one</li>");
    expect(html).toContain("<code>inline</code>");
    expect(html).toContain("echo hello");
  });

  test.each([
    ["ordinary blockquote", "> This is a quote."],
    ["unknown marker", "> [!SUCCESS]\n> Body"],
    ["marker with same-line body", "> [!NOTE] Body"],
    ["empty alert", "> [!NOTE]"],
    ["marker after other text", "> Before\n> [!NOTE]\n> Body"],
  ])("leaves %s unchanged", (_name: string, markdown: string) => {
    const html = render(markdown);

    expect(html).toContain("<blockquote>");
    expect(html).not.toContain("markdown-alert");
  });

  test.each([
    ["list", "- > [!NOTE]\n  > Body"],
    ["blockquote", "> > [!NOTE]\n> > Body"],
  ])(
    "does not transform an alert nested in a %s",
    (_name: string, markdown: string) => {
      const html = render(markdown);

      expect(html).toContain("[!NOTE]");
      expect(html).not.toContain("markdown-alert");
    },
  );

  test("accepts case-insensitive markers, trailing whitespace, and CRLF", () => {
    const html = render("> [!note]  \r\n> Body");

    expect(html).toContain("markdown-alert-note");
    expect(html).toContain(">Body</p>");
    expect(html).not.toContain("<br>");
    expect(html).not.toContain("[!note]");
  });

  test("supports a marker paragraph separated from its body", () => {
    const html = render("> [!TIP]\n>\n> A separate paragraph.");

    expect(html).toContain("markdown-alert-tip");
    expect(html).toContain("<p>A separate paragraph.</p>");
  });
});
