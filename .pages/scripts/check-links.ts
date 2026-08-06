import { parse, type DefaultTreeAdapterTypes } from "parse5";

const SITE_ORIGIN = "https://daiksud.github.io";
const SITE_HOSTNAME = new URL(SITE_ORIGIN).hostname;
const BASE_PATH = "/gh-qw/";
const BASE_ROOT = BASE_PATH.slice(0, -1);
const GITHUB_ORIGIN = "https://github.com";
const REPOSITORY_EDIT_ROOT = "/daiksud/gh-qw/edit/";

const DIST_DIRECTORY = `${import.meta.dir}/../dist`;
const DOCS_DIRECTORY = `${import.meta.dir}/../../docs`;

type AttributeName = "href" | "src";

export interface LinkReference {
  readonly element: string;
  readonly attribute: AttributeName;
  readonly value: string;
}

export interface Failure {
  readonly source: string;
  readonly reference?: LinkReference;
  readonly message: string;
}

export interface DocumentMetadata {
  readonly references: readonly LinkReference[];
  readonly anchorIds: ReadonlySet<string>;
  readonly duplicateAnchorIds: ReadonlySet<string>;
}

export interface GeneratedSite {
  readonly htmlFiles: ReadonlyMap<string, string>;
  readonly outputFiles: ReadonlySet<string>;
  readonly sourceDocs: ReadonlySet<string>;
  readonly readOutputFile: (relativePath: string) => Promise<string>;
}

export interface ValidationResult {
  readonly failures: readonly Failure[];
  readonly htmlFileCount: number;
  readonly localReferenceCount: number;
  readonly editLinkCount: number;
}

function attributeValue(
  node: DefaultTreeAdapterTypes.Element,
  name: string,
): string | undefined {
  return node.attrs.find((attribute) => attribute.name === name)?.value;
}

/**
 * Parse references and anchors with an HTML parser so text, comments, and
 * script contents cannot masquerade as element attributes.
 */
export function extractDocumentMetadata(markup: string): DocumentMetadata {
  const document = parse(markup);
  const references: LinkReference[] = [];
  const anchorIds = new Set<string>();
  const duplicateAnchorIds = new Set<string>();

  function addAnchorId(identifier: string): void {
    if (identifier === "") return;
    if (anchorIds.has(identifier)) {
      duplicateAnchorIds.add(identifier);
    } else {
      anchorIds.add(identifier);
    }
  }

  function visit(node: DefaultTreeAdapterTypes.Node): void {
    if ("attrs" in node) {
      for (const attribute of node.attrs) {
        if (attribute.name === "href" || attribute.name === "src") {
          references.push({
            element: node.tagName,
            attribute: attribute.name,
            value: attribute.value,
          });
        }
      }

      const id = attributeValue(node, "id");
      if (id !== undefined) addAnchorId(id);

      if (node.tagName === "a") {
        const name = attributeValue(node, "name");
        if (name !== undefined) addAnchorId(name);
      }
    }

    if ("childNodes" in node) {
      for (const child of node.childNodes) visit(child);
    }

    if ("content" in node) {
      visit(node.content);
    }
  }

  visit(document);
  return { references, anchorIds, duplicateAnchorIds };
}

async function scanFiles(
  directory: string,
  pattern: string,
): Promise<string[]> {
  const glob = new Bun.Glob(pattern);
  const paths: string[] = [];

  try {
    for await (const path of glob.scan({
      cwd: directory,
      dot: true,
      onlyFiles: true,
    })) {
      paths.push(path.replaceAll("\\", "/"));
    }
  } catch (error) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") {
      return [];
    }

    throw error;
  }

  return paths.sort();
}

function htmlPathToPublicPath(relativePath: string): string {
  if (relativePath === "index.html") {
    return BASE_PATH;
  }

  if (relativePath.endsWith("/index.html")) {
    return `${BASE_PATH}${relativePath.slice(0, -"index.html".length)}`;
  }

  return `${BASE_PATH}${relativePath}`;
}

function expectedEditTarget(
  relativeHtmlPath: string,
  sourceDocs: ReadonlySet<string>,
): string | undefined {
  let target: string;
  if (relativeHtmlPath === "index.html") {
    target = "docs/README.md";
  } else if (relativeHtmlPath.endsWith("/index.html")) {
    target = `docs/${relativeHtmlPath.slice(0, -"index.html".length)}README.md`;
  } else {
    return undefined;
  }

  return sourceDocs.has(target) ? target : undefined;
}

function hasCredentials(url: URL): boolean {
  return url.username !== "" || url.password !== "";
}

function decodeSafePath(
  pathname: string,
  source: string,
  reference: LinkReference,
  addFailure: (
    source: string,
    message: string,
    reference?: LinkReference,
  ) => void,
  description: string,
): string | undefined {
  const decodedSegments: string[] = [];

  for (const encodedSegment of pathname.split("/")) {
    let segment: string;
    try {
      segment = decodeURIComponent(encodedSegment);
    } catch {
      addFailure(
        source,
        `${description} contains malformed percent encoding`,
        reference,
      );
      return undefined;
    }

    if (
      segment === "." ||
      segment === ".." ||
      segment.includes("/") ||
      segment.includes("\\") ||
      segment.includes("\0")
    ) {
      addFailure(
        source,
        `${description} contains an unsafe path segment`,
        reference,
      );
      return undefined;
    }

    decodedSegments.push(segment);
  }

  return decodedSegments.join("/");
}

function parseEditTarget(
  url: URL,
  source: string,
  reference: LinkReference,
  sourceDocs: ReadonlySet<string>,
  addFailure: (
    source: string,
    message: string,
    reference?: LinkReference,
  ) => void,
): string | undefined {
  if (
    url.hostname !== "github.com" ||
    !url.pathname.startsWith(REPOSITORY_EDIT_ROOT)
  ) {
    return undefined;
  }

  if (url.origin !== GITHUB_ORIGIN || hasCredentials(url)) {
    addFailure(
      source,
      `edit URL must use the canonical ${GITHUB_ORIGIN} origin`,
      reference,
    );
    return "";
  }

  const pathname = decodeSafePath(
    url.pathname,
    source,
    reference,
    addFailure,
    "edit URL",
  );
  if (pathname === undefined) return "";

  const match =
    /^\/daiksud\/gh-qw\/edit\/main\/(docs\/(?:[^/]+\/)*README\.md)$/.exec(
      pathname,
    );
  if (match === null || url.search !== "" || url.hash !== "") {
    addFailure(
      source,
      "edit URL must match https://github.com/daiksud/gh-qw/edit/main/docs/**/README.md",
      reference,
    );
    return "";
  }

  const target = match[1]!;
  if (!sourceDocs.has(target)) {
    addFailure(
      source,
      `edit URL points to a missing source document: ${target}`,
      reference,
    );
    return "";
  }

  return target;
}

function outputCandidates(
  pathname: string,
  source: string,
  reference: LinkReference,
  addFailure: (
    source: string,
    message: string,
    reference?: LinkReference,
  ) => void,
): string[] | undefined {
  if (pathname !== BASE_ROOT && !pathname.startsWith(BASE_PATH)) {
    addFailure(
      source,
      `local URL escapes the ${BASE_PATH} base path`,
      reference,
    );
    return undefined;
  }

  const encodedRelativePath =
    pathname === BASE_ROOT ? "" : pathname.slice(BASE_PATH.length);
  const relativePath = decodeSafePath(
    encodedRelativePath,
    source,
    reference,
    addFailure,
    "local URL",
  );
  if (relativePath === undefined) return undefined;

  if (relativePath === "") {
    return ["index.html"];
  }

  if (relativePath.endsWith("/")) {
    return [`${relativePath}index.html`, `${relativePath.slice(0, -1)}.html`];
  }

  return [relativePath, `${relativePath}.html`, `${relativePath}/index.html`];
}

function fragmentIdentifier(hash: string): string | undefined {
  if (hash === "" || hash === "#") {
    return undefined;
  }

  const encodedFragment = hash.slice(1).split(":~:text=", 1)[0]!;
  if (encodedFragment === "") {
    return undefined;
  }

  return decodeURIComponent(encodedFragment);
}

function isUnsafeReference(url: URL, reference: LinkReference): boolean {
  if (
    url.protocol === "javascript:" ||
    url.protocol === "vbscript:" ||
    url.protocol === "file:"
  ) {
    return true;
  }

  if (url.protocol !== "data:") return false;

  return !(
    reference.element === "img" &&
    reference.attribute === "src" &&
    reference.value.trimStart().toLowerCase().startsWith("data:image/")
  );
}

export async function validateGeneratedSite(
  site: GeneratedSite,
): Promise<ValidationResult> {
  const failures: Failure[] = [];
  const metadataCache = new Map<string, DocumentMetadata>();
  const seenEditTargets = new Set<string>();
  let localReferenceCount = 0;

  function addFailure(
    source: string,
    message: string,
    reference?: LinkReference,
  ): void {
    failures.push({ source, reference, message });
  }

  async function metadataFor(relativePath: string): Promise<DocumentMetadata> {
    const cached = metadataCache.get(relativePath);
    if (cached !== undefined) return cached;

    const markup =
      site.htmlFiles.get(relativePath) ??
      (await site.readOutputFile(relativePath));
    const metadata = extractDocumentMetadata(markup);
    metadataCache.set(relativePath, metadata);

    for (const identifier of metadata.duplicateAnchorIds) {
      addFailure(relativePath, `duplicate anchor identifier: ${identifier}`);
    }

    return metadata;
  }

  async function validateLocalReference(
    url: URL,
    source: string,
    reference: LinkReference,
  ): Promise<void> {
    const candidates = outputCandidates(
      url.pathname,
      source,
      reference,
      addFailure,
    );
    if (candidates === undefined) return;

    const target = candidates.find((candidate) =>
      site.outputFiles.has(candidate),
    );
    if (target === undefined) {
      addFailure(
        source,
        `no generated target found (tried ${candidates.join(", ")})`,
        reference,
      );
      return;
    }

    if (
      url.hash === "" ||
      !(target.endsWith(".html") || target.endsWith(".svg"))
    ) {
      return;
    }

    let identifier: string | undefined;
    try {
      identifier = fragmentIdentifier(url.hash);
    } catch {
      addFailure(
        source,
        "fragment contains malformed percent encoding",
        reference,
      );
      return;
    }

    if (identifier === undefined) return;

    const targetMetadata = await metadataFor(target);
    if (!targetMetadata.anchorIds.has(identifier)) {
      addFailure(
        source,
        `fragment #${identifier} does not exist in ${target}`,
        reference,
      );
    }
  }

  for (const [relativeHtmlPath] of site.htmlFiles) {
    const metadata = await metadataFor(relativeHtmlPath);
    const pageUrl = new URL(
      htmlPathToPublicPath(relativeHtmlPath),
      SITE_ORIGIN,
    );
    const pageEditTargets = new Set<string>();

    for (const reference of metadata.references) {
      let url: URL;
      try {
        url = new URL(reference.value, pageUrl);
      } catch {
        addFailure(relativeHtmlPath, "URL cannot be parsed", reference);
        continue;
      }

      const editTarget = parseEditTarget(
        url,
        relativeHtmlPath,
        reference,
        site.sourceDocs,
        addFailure,
      );
      if (editTarget !== undefined) {
        if (editTarget !== "") {
          seenEditTargets.add(editTarget);
          pageEditTargets.add(editTarget);
        }
        continue;
      }

      if (isUnsafeReference(url, reference)) {
        addFailure(
          relativeHtmlPath,
          `unsafe URL protocol: ${url.protocol}`,
          reference,
        );
        continue;
      }

      if (
        url.hostname === SITE_HOSTNAME &&
        (url.origin !== SITE_ORIGIN || hasCredentials(url))
      ) {
        addFailure(
          relativeHtmlPath,
          `site URL must use the canonical ${SITE_ORIGIN} origin`,
          reference,
        );
        continue;
      }

      if (
        (url.protocol !== "http:" && url.protocol !== "https:") ||
        url.origin !== SITE_ORIGIN
      ) {
        continue;
      }

      localReferenceCount += 1;
      await validateLocalReference(url, relativeHtmlPath, reference);
    }

    const expected = expectedEditTarget(relativeHtmlPath, site.sourceDocs);
    if (expected !== undefined && !pageEditTargets.has(expected)) {
      addFailure(relativeHtmlPath, `missing edit URL for ${expected}`);
    }
    for (const actual of pageEditTargets) {
      if (expected !== actual) {
        addFailure(
          relativeHtmlPath,
          `edit URL targets ${actual}; expected ${expected ?? "no source document"}`,
        );
      }
    }
  }

  for (const sourceDoc of site.sourceDocs) {
    if (!seenEditTargets.has(sourceDoc)) {
      addFailure("dist", `no generated page links to edit ${sourceDoc}`);
    }
  }

  return {
    failures,
    htmlFileCount: site.htmlFiles.size,
    localReferenceCount,
    editLinkCount: seenEditTargets.size,
  };
}

async function main(): Promise<void> {
  const [htmlPaths, allOutputPaths, sourceReadmePaths] = await Promise.all([
    scanFiles(DIST_DIRECTORY, "**/*.html"),
    scanFiles(DIST_DIRECTORY, "**/*"),
    scanFiles(DOCS_DIRECTORY, "**/README.md"),
  ]);

  if (htmlPaths.length === 0) {
    console.error(
      `No generated HTML found in ${DIST_DIRECTORY}. Run bun run build first.`,
    );
    process.exitCode = 1;
    return;
  }

  if (sourceReadmePaths.length === 0) {
    console.error(`No source documentation found in ${DOCS_DIRECTORY}.`);
    process.exitCode = 1;
    return;
  }

  const fileContents = new Map<string, Promise<string>>();
  function readOutputFile(relativePath: string): Promise<string> {
    let contents = fileContents.get(relativePath);
    if (contents === undefined) {
      contents = Bun.file(`${DIST_DIRECTORY}/${relativePath}`).text();
      fileContents.set(relativePath, contents);
    }
    return contents;
  }

  const htmlFiles = new Map(
    await Promise.all(
      htmlPaths.map(
        async (relativePath) =>
          [relativePath, await readOutputFile(relativePath)] as const,
      ),
    ),
  );
  const result = await validateGeneratedSite({
    htmlFiles,
    outputFiles: new Set(allOutputPaths),
    sourceDocs: new Set(sourceReadmePaths.map((path) => `docs/${path}`)),
    readOutputFile,
  });

  if (result.failures.length > 0) {
    console.error(
      `Generated site link check failed with ${result.failures.length} error${
        result.failures.length === 1 ? "" : "s"
      }:`,
    );
    for (const failure of result.failures) {
      const reference = failure.reference
        ? ` (<${failure.reference.element}> ${failure.reference.attribute}=${JSON.stringify(
            failure.reference.value,
          )})`
        : "";
      console.error(`- ${failure.source}${reference}: ${failure.message}`);
    }
    process.exitCode = 1;
    return;
  }

  console.log(
    `Checked ${result.htmlFileCount} HTML files, ${result.localReferenceCount} local references, and ${result.editLinkCount} edit links.`,
  );
}

if (import.meta.main) {
  await main();
}
