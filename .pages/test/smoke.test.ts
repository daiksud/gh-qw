import { describe, expect, test } from "vite-plus/test";

import {
  collectFirstPartyAssetPaths,
  distPathToPublicPath,
  evaluateResponse,
} from "../scripts/smoke";

describe("Astro runtime smoke helpers", () => {
  test.each([
    ["index.html", "/gh-qw/"],
    ["concept/index.html", "/gh-qw/concept/"],
    ["reference/cli/index.html", "/gh-qw/reference/cli/"],
  ])("maps %s to %s", (relativePath, expectedPath) => {
    expect(distPathToPublicPath(relativePath)).toBe(expectedPath);
  });

  test("accepts a healthy HTML response with a title", () => {
    expect(
      evaluateResponse(
        "/gh-qw/",
        200,
        "text/html; charset=utf-8",
        "<html><head><title>gh-qw</title></head></html>",
        { kind: "html", status: 200 },
      ),
    ).toEqual([]);
  });

  test("rejects an HTML response without a title", () => {
    expect(
      evaluateResponse(
        "/gh-qw/",
        200,
        "text/html",
        "<html><head></head></html>",
        { kind: "html", status: 200 },
      ),
    ).toContain("/gh-qw/: response has no non-empty <title>");
  });

  test("rejects an asset that is an HTML error page", () => {
    expect(
      evaluateResponse(
        "/gh-qw/_astro/site.js",
        200,
        "text/html",
        "<html>Not found</html>",
        { kind: "asset", status: 200 },
      ),
    ).toContain(
      "/gh-qw/_astro/site.js: expected a non-HTML asset, received text/html",
    );
  });

  test("accepts an HTML 404 response", () => {
    expect(
      evaluateResponse(
        "/gh-qw/missing/",
        404,
        "text/html; charset=utf-8",
        "<html><body>Not found</body></html>",
        { kind: "not-found", status: 404 },
      ),
    ).toEqual([]);
  });

  test("collects only first-party page assets from the homepage", () => {
    expect(
      collectFirstPartyAssetPaths(`
        <link rel="stylesheet" href="/gh-qw/_astro/site.css">
        <script src="/gh-qw/_astro/site.js"></script>
        <link rel="icon" href="/gh-qw/favicon.svg">
        <img src="https://example.com/external.svg">
      `),
    ).toEqual([
      "/gh-qw/_astro/site.css",
      "/gh-qw/_astro/site.js",
      "/gh-qw/favicon.svg",
    ]);
  });
});
