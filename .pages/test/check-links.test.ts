import { describe, expect, test } from "vite-plus/test";

import {
  extractDocumentMetadata,
  validateGeneratedSite,
  type ValidationResult,
} from "../scripts/check-links";

const EDIT_LINK = "https://github.com/daiksud/gh-qw/edit/main/docs/README.md";
const EDIT_MDX_LINK = "https://github.com/daiksud/gh-qw/edit/main/docs/README.mdx";

function page(...body: string[]): string {
  return `<!doctype html><html><body>${body.join("")}</body></html>`;
}

async function validateRoot(
  body: string,
  extraOutputs: ReadonlyMap<string, string> = new Map(),
  sourceDocs: ReadonlySet<string> = new Set(["docs/README.md"]),
  editLink: string = EDIT_LINK,
): Promise<ValidationResult> {
  const index = page(`<a href="${editLink}">Edit</a>`, body);
  const outputContents = new Map<string, string>([
    ["index.html", index],
    ...extraOutputs,
  ]);

  return validateGeneratedSite({
    htmlFiles: new Map([["index.html", index]]),
    outputFiles: new Set(outputContents.keys()),
    sourceDocs,
    async readOutputFile(relativePath) {
      const contents = outputContents.get(relativePath);
      if (contents === undefined)
        throw new Error(`Missing fixture: ${relativePath}`);
      return contents;
    },
  });
}

describe("generated site link checker", () => {
  test("extracts only real attributes and decodes HTML entities", () => {
    const metadata = extractDocumentMetadata(
      page(
        '<p>Literal href="/fake/" id=phantom.</p>',
        '<!-- <a href="/comment" id="comment"></a> -->',
        '<script>const example = `<a href="/script" id="script">`;</script>',
        '<a href="/gh-qw/?first=1&amp;second=2" id="real" name="legacy">Link</a>',
      ),
    );

    expect(metadata.references).toEqual([
      {
        element: "a",
        attribute: "href",
        value: "/gh-qw/?first=1&second=2",
      },
    ]);
    expect([...metadata.anchorIds]).toEqual(["real", "legacy"]);
    expect(metadata.duplicateAnchorIds.size).toBe(0);
  });

  test("accepts valid local, external, edit, fragment, and image references", async () => {
    const result = await validateRoot(
      [
        '<h1 id="start">Start</h1>',
        '<a href="/gh-qw/#start">Top</a>',
        '<a href="mailto:maintainer@example.com">Mail</a>',
        '<img src="/gh-qw/logo.svg" alt="">',
        '<img src="data:image/png;base64,AA==" alt="">',
      ].join(""),
      new Map([["logo.svg", "<svg></svg>"]]),
    );

    expect(result.failures).toEqual([]);
    expect(result.htmlFileCount).toBe(1);
    expect(result.localReferenceCount).toBe(2);
    expect(result.editLinkCount).toBe(1);
  });

  test.each([
    [
      "insecure origin",
      "http://github.com/daiksud/gh-qw/edit/main/docs/README.md",
    ],
    [
      "credentials",
      "https://user@github.com/daiksud/gh-qw/edit/main/docs/README.md",
    ],
    [
      "custom port",
      "https://github.com:444/daiksud/gh-qw/edit/main/docs/README.md",
    ],
  ])("rejects an edit URL with %s", async (_name, invalidEditLink) => {
    const result = await validateRoot(
      `<a href="${invalidEditLink}">Bad edit link</a>`,
    );

    expect(result.failures.map((failure) => failure.message)).toContain(
      "edit URL must use the canonical https://github.com origin",
    );
  });

  test.each([
    [
      "an insecure site origin",
      "http://daiksud.github.io/gh-qw/",
      "site URL must use the canonical https://daiksud.github.io origin",
    ],
    [
      "site credentials",
      "https://user@daiksud.github.io/gh-qw/",
      "site URL must use the canonical https://daiksud.github.io origin",
    ],
    [
      "an encoded path separator",
      "/gh-qw/reference%2Fcli/",
      "local URL contains an unsafe path segment",
    ],
    ["a script URL", "javascript:alert(1)", "unsafe URL protocol: javascript:"],
    ["a file URL", "file:///etc/passwd", "unsafe URL protocol: file:"],
    ["an HTML data URL", "data:text/html,test", "unsafe URL protocol: data:"],
  ])("rejects %s", async (_name, href, expectedMessage) => {
    const result = await validateRoot(`<a href="${href}">Unsafe</a>`);

    expect(result.failures.map((failure) => failure.message)).toContain(
      expectedMessage,
    );
  });

  test("reports missing targets and fragments without accepting text as an id", async () => {
    const result = await validateRoot(
      [
        '<p>Examples: id=phantom and href="/gh-qw/not-a-link/".</p>',
        '<a href="/gh-qw/#phantom">Missing fragment</a>',
        '<a href="/gh-qw/missing/">Missing page</a>',
      ].join(""),
    );

    expect(result.failures.map((failure) => failure.message)).toEqual([
      "fragment #phantom does not exist in index.html",
      "no generated target found (tried missing/index.html)",
    ]);
  });

  test("does not treat a flat HTML file as a directory URL", async () => {
    const result = await validateRoot(
      '<a href="/gh-qw/missing/">Missing directory</a>',
      new Map([["missing.html", "<!doctype html><html></html>"]]),
    );

    expect(result.failures.map((failure) => failure.message)).toContain(
      "no generated target found (tried missing/index.html)",
    );
  });

  test("accepts Astro's flat 404 output for the 404 directory URL", async () => {
    const result = await validateRoot(
      '<a href="/gh-qw/404/">Not found</a>',
      new Map([["404.html", "<!doctype html><html></html>"]]),
    );

    expect(result.failures).toEqual([]);
  });

  test("accepts an MDX edit URL for an MDX source document", async () => {
    const result = await validateRoot(
      "",
      new Map(),
      new Set(["docs/README.mdx"]),
      EDIT_MDX_LINK,
    );

    expect(result.failures).toEqual([]);
    expect(result.editLinkCount).toBe(1);
  });

  test("reports duplicate anchor identifiers", async () => {
    const result = await validateRoot(
      '<h2 id="duplicate">One</h2><p id="duplicate">Two</p>',
    );

    expect(result.failures.map((failure) => failure.message)).toContain(
      "duplicate anchor identifier: duplicate",
    );
  });

  test("checks references inside inert templates that can be activated at runtime", async () => {
    const result = await validateRoot(
      '<template><a href="/gh-qw/missing/">Deferred link</a></template>',
    );

    expect(result.failures.map((failure) => failure.message)).toContain(
      "no generated target found (tried missing/index.html)",
    );
  });
});
