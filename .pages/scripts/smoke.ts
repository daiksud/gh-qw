import { glob } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { preview } from "astro";

import { extractDocumentMetadata } from "./check-links.ts";

const BASE_PATH = "/gh-qw";
const DIST_DIRECTORY = join(import.meta.dirname, "../dist");
const PAGEFIND_SCRIPT_PATH = `${BASE_PATH}/pagefind/pagefind.js`;
const NOT_FOUND_PATH = `${BASE_PATH}/__gh_qw_smoke_not_found__/`;
const REQUEST_TIMEOUT_MS = 10_000;

type ResponseKind = "html" | "asset" | "not-found";

export interface ResponseExpectation {
  readonly kind: ResponseKind;
  readonly status: number;
}

export function distPathToPublicPath(relativePath: string): string {
  const normalizedPath = relativePath.replaceAll("\\", "/");

  if (normalizedPath === "index.html") {
    return `${BASE_PATH}/`;
  }

  if (normalizedPath.endsWith("/index.html")) {
    return `${BASE_PATH}/${normalizedPath.slice(0, -"index.html".length)}`;
  }

  return `${BASE_PATH}/${normalizedPath}`;
}

export function evaluateResponse(
  path: string,
  status: number,
  contentType: string | null,
  body: string,
  expectation: ResponseExpectation,
): string[] {
  const failures: string[] = [];
  const normalizedContentType =
    contentType?.split(";", 1)[0]?.trim().toLowerCase() ?? "";

  if (status !== expectation.status) {
    failures.push(
      `${path}: expected HTTP ${expectation.status}, received ${status}`,
    );
  }

  if (expectation.kind === "html") {
    if (!normalizedContentType.startsWith("text/html")) {
      failures.push(
        `${path}: expected text/html, received ${contentType ?? "no content type"}`,
      );
    }

    const title = /<title\b[^>]*>([\s\S]*?)<\/title>/i.exec(body)?.[1]
      ?.replace(/<[^>]*>/g, "")
      .trim();
    if (title === undefined || title === "") {
      failures.push(`${path}: response has no non-empty <title>`);
    }
  } else if (expectation.kind === "asset") {
    if (normalizedContentType === "" || normalizedContentType === "text/html") {
      failures.push(
        `${path}: expected a non-HTML asset, received ${contentType ?? "no content type"}`,
      );
    }

    if (body.trim() === "") {
      failures.push(`${path}: response body is empty`);
    }
  } else if (!normalizedContentType.startsWith("text/html")) {
    failures.push(
      `${path}: expected the not-found response to be text/html, received ${contentType ?? "no content type"}`,
    );
  }

  return failures;
}

export function collectFirstPartyAssetPaths(html: string): string[] {
  const homepageUrl = new URL(`https://daiksud.github.io${BASE_PATH}/`);
  const paths = new Set<string>();

  for (const reference of extractDocumentMetadata(html).references) {
    const url = new URL(reference.value, homepageUrl);
    if (
      url.origin === homepageUrl.origin &&
      (url.pathname.startsWith(`${BASE_PATH}/_astro/`) ||
        url.pathname === `${BASE_PATH}/favicon.svg`)
    ) {
      paths.add(url.pathname);
    }
  }

  return [...paths].sort();
}

interface CheckedResponse {
  readonly body: string;
  readonly failures: readonly string[];
}

async function fetchAndEvaluate(
  origin: string,
  path: string,
  expectation: ResponseExpectation,
): Promise<CheckedResponse> {
  const url = `${origin}${path}`;

  try {
    const response = await fetch(url, {
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    const body = await response.text();
    return {
      body,
      failures: evaluateResponse(
        path,
        response.status,
        response.headers.get("content-type"),
        body,
        expectation,
      ),
    };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      body: "",
      failures: [`${path}: request failed: ${message}`],
    };
  }
}

async function htmlRoutes(): Promise<string[]> {
  const paths: string[] = [];

  for await (const path of glob("**/*.html", { cwd: DIST_DIRECTORY })) {
    const normalizedPath = path.replaceAll("\\", "/");
    if (normalizedPath !== "404.html") {
      paths.push(normalizedPath);
    }
  }

  return paths.sort();
}

async function main(): Promise<void> {
  const routes = await htmlRoutes();
  if (routes.length === 0) {
    console.error(`No generated HTML found in ${DIST_DIRECTORY}.`);
    process.exitCode = 1;
    return;
  }

  const server = await preview({
    root: fileURLToPath(new URL("../", import.meta.url)),
    server: {
      host: "127.0.0.1",
      port: 0,
    },
  });
  const origin = `http://${server.host ?? "127.0.0.1"}:${server.port}`;
  const failures: string[] = [];
  let assetCount = 0;

  try {
    let homepageHtml = "";

    for (const route of routes) {
      const path = distPathToPublicPath(route);
      const result = await fetchAndEvaluate(origin, path, {
        kind: "html",
        status: 200,
      });
      failures.push(...result.failures);
      if (route === "index.html") {
        homepageHtml = result.body;
      }
    }

    const assetPaths = collectFirstPartyAssetPaths(homepageHtml);
    assetCount = assetPaths.length;
    if (assetPaths.length === 0) {
      failures.push("homepage: no first-party CSS, JavaScript, or favicon assets found");
    }
    for (const path of assetPaths) {
      const result = await fetchAndEvaluate(origin, path, {
        kind: "asset",
        status: 200,
      });
      failures.push(...result.failures);
    }

    failures.push(
      ...(
        await fetchAndEvaluate(origin, PAGEFIND_SCRIPT_PATH, {
          kind: "asset",
          status: 200,
        })
      ).failures,
    );
    failures.push(
      ...(
        await fetchAndEvaluate(origin, NOT_FOUND_PATH, {
          kind: "not-found",
          status: 404,
        })
      ).failures,
    );
  } finally {
    await server.stop();
  }

  if (failures.length > 0) {
    console.error(`Astro runtime smoke test failed with ${failures.length} error(s):`);
    for (const failure of failures) {
      console.error(`- ${failure}`);
    }
    process.exitCode = 1;
    return;
  }

  console.log(
    `Astro runtime smoke test passed for ${routes.length} HTML routes and ${assetCount} first-party assets.`,
  );
}

if (import.meta.main) {
  await main();
}
