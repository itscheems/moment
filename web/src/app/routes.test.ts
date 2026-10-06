import { describe, expect, it } from "vitest";

import {
  ARTICLE_COMPOSE_PATH,
  COMPOSE_PATH,
  articleDraftIdFromPath,
  articleDraftPath,
  articleFocusPath,
  articleFocusIdFromPath,
  isComposePath,
  movedComposePath,
  resolveRoute,
} from "./routes";

describe("route boundaries", () => {
  it.each([
    ["/", "home"],
    ["/m/01JABC", "detail"],
    ["/search", "search"],
    ["/archive", "archive"],
    ["/login", "login"],
    ["/compose", "owner-compose"],
    ["/compose/drafts", "owner-compose"],
    // Still resolves, so an entry already in somebody's history renders
    // while the app corrects its address.
    ["/owner/compose", "owner-compose"],
    ["/owner/m/01JABC/edit", "owner-edit"],
    ["/owner/settings", "owner-settings"],
    ["/profile", "profile"],
    ["/itscheems", "profile"],
    ["/missing", "profile"],
    ["/ab", "not-found"],
    ["/Owner", "not-found"],
    ["/9lives", "not-found"],
    ["/has-dash", "not-found"],
    ["/nested/path", "not-found"],
  ])("maps %s to %s", (pathname, id) => {
    expect(resolveRoute(pathname).id).toBe(id);
  });

  // Handles moved to the site root, so `/username` now overlaps every
  // single-segment page. `resolveRoute` returns the first match, which only
  // stays correct while the profile route is the last entry in the array.
  it.each([
    ["/search", "search"],
    ["/search/", "search"],
    ["/archive", "archive"],
    ["/archive/", "archive"],
    ["/login", "login"],
    ["/owner/settings", "owner-settings"],
    ["/compose", "owner-compose"],
    ["/compose/", "owner-compose"],
    ["/m/01JABC", "detail"],
    ["/", "home"],
  ])("keeps %s out of the root handle route (%s)", (pathname, id) => {
    expect(resolveRoute(pathname).id).toBe(id);
    expect(resolveRoute(pathname).id).not.toBe("profile");
  });
});

describe("published article focus addresses", () => {
  it("keeps the published id in a public author/article link and provides its detail fallback", () => {
    const href = articleFocusPath("owner", "01K2ABC");
    expect(href).toBe("/owner/article/01K2ABC");
    expect(resolveRoute(href)).toMatchObject({
      id: "article-focus",
      title: "文章",
      href: "/m/01K2ABC",
    });
    expect(resolveRoute(href).ownerOnly).not.toBe(true);
    expect(articleFocusIdFromPath(`${href}/`)).toBe("01K2ABC");
  });

  it("decodes the id while rejecting adjacent routes and invalid handles", () => {
    expect(articleFocusIdFromPath(articleFocusPath("owner", "id /"))).toBe(
      "id /",
    );
    expect(articleFocusIdFromPath("/owner/article/%invalid")).toBe("%invalid");
    for (const path of [
      "/compose/article",
      "/compose/article/draft-id",
      "/Owner/article/id",
      "/a/article/id",
      "/owner/article/id/more",
      "/owner/article/",
    ]) {
      expect(articleFocusIdFromPath(path)).toBeNull();
      expect(resolveRoute(path).id).not.toBe("article-focus");
    }
  });
});

// Composing moved out from under /owner: it is what the site is for, not an
// administrative corner of it. The old addresses still resolve so history
// entries render, and say where they moved to.
describe("the address composing moved from", () => {
  it.each([
    ["/owner/compose", "/compose"],
    ["/owner/compose/", "/compose"],
    ["/owner/compose/drafts", "/compose/drafts"],
    ["/owner/compose/drafts/", "/compose/drafts"],
  ])("sends %s to %s", (from, to) => {
    expect(movedComposePath(from)).toBe(to);
  });

  it("leaves every other address alone", () => {
    for (const pathname of [
      "/",
      COMPOSE_PATH,
      "/compose/drafts",
      "/owner/settings",
    ]) {
      expect(movedComposePath(pathname)).toBeNull();
    }
  });
});

// `/owner/login` was the sign-in address until signing in stopped being an
// owner action. It is gone rather than aliased: an address that keeps working
// keeps being linked, and this one names a thing the product no longer has.
describe("the retired owner sign-in address", () => {
  it("no longer resolves to the sign-in page", () => {
    expect(resolveRoute("/owner/login").id).not.toBe("login");
  });

  // And is not swallowed by the root handle route on the way out: two
  // segments, so it lands where any other address nobody serves lands.
  it("is a page that does not exist rather than somebody's profile", () => {
    expect(resolveRoute("/owner/login").id).toBe("not-found");
  });
});

describe("where an article is written", () => {
  it("answers under compose rather than under /owner", () => {
    expect(ARTICLE_COMPOSE_PATH).toBe("/compose/article");
    expect(resolveRoute(ARTICLE_COMPOSE_PATH).id).toBe("owner-article");
    expect(resolveRoute("/compose/article/").id).toBe("owner-article");
  });

  it("carries the draft being edited in the address", () => {
    expect(resolveRoute("/compose/article/01K2ABC").id).toBe("owner-article");
    expect(articleDraftIdFromPath("/compose/article/01K2ABC")).toBe("01K2ABC");
    expect(articleDraftIdFromPath("/compose/article/01K2ABC/")).toBe("01K2ABC");
  });

  it("means a new article when it names no draft", () => {
    expect(articleDraftIdFromPath(ARTICLE_COMPOSE_PATH)).toBeNull();
    expect(articleDraftIdFromPath("/compose")).toBeNull();
  });

  it("builds the address for a draft with the id escaped", () => {
    expect(articleDraftPath("01K2/ABC")).toBe("/compose/article/01K2%2FABC");
  });

  it("does not take the moment composer's addresses with it", () => {
    expect(resolveRoute(COMPOSE_PATH).id).toBe("owner-compose");
    expect(resolveRoute("/compose/drafts").id).toBe("owner-compose");
    expect(isComposePath(ARTICLE_COMPOSE_PATH)).toBe(false);
  });

  it("is the owner's, the way every other composing address is", () => {
    expect(resolveRoute(ARTICLE_COMPOSE_PATH).ownerOnly).toBe(true);
  });
});
