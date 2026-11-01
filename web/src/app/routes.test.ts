import { describe, expect, it } from "vitest";

import {
  ARTICLE_COMPOSE_PATH,
  COMPOSE_PATH,
  articleDraftIdFromPath,
  articleDraftPath,
  articleFocusPath,
  articleFocusIdFromPath,
  commentDetailFromPath,
  commentDetailPath,
  isComposePath,
  profileFeedPath,
  profileFromPath,
  resolveRoute,
} from "./routes";

describe("route boundaries", () => {
  it("opens comment permalinks independently from post editing and profiles", () => {
    const path = commentDetailPath("post", "reply");
    expect(path).toBe("/m/post/comments/reply");
    expect(commentDetailFromPath(path)).toEqual({
      postId: "post",
      commentId: "reply",
    });
    expect(resolveRoute(path)).toMatchObject({
      id: "comment-detail",
      href: "/m/post",
    });
    expect(commentDetailPath("post", "root", true)).toBe(
      "/m/post/comments/root?view=thread",
    );
    expect(commentDetailFromPath("/m/post/edit")).toBeNull();
  });
  it.each([
    ["/", "home"],
    ["/m/01JABC", "detail"],
    ["/search", "search"],
    ["/archive", "archive"],
    ["/login", "login"],
    ["/register", "register"],
    ["/register/", "register"],
    ["/compose", "compose"],
    ["/compose/post/media", "compose"],
    ["/compose/drafts", "compose"],
    ["/owner/compose", "not-found"],
    ["/m/01JABC/edit", "edit"],
    ["/settings/account", "settings"],
    ["/settings/profile", "profile-edit"],
    ["/settings/profile/", "profile-edit"],
    ["/profile", "profile"],
    ["/itscheems", "profile"],
    ["/itscheems/all", "profile"],
    ["/itscheems/posts/", "profile"],
    ["/itscheems/articles", "profile"],
    ["/missing", "profile"],
    ["/ab", "not-found"],
    ["/Owner", "not-found"],
    ["/9lives", "not-found"],
    ["/has-dash", "not-found"],
    ["/nested/path", "not-found"],
    ["/itscheems/unknown", "not-found"],
    ["/itscheems/articles/more", "not-found"],
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
    ["/register", "register"],
    ["/settings/account", "settings"],
    ["/compose", "compose"],
    ["/compose/", "compose"],
    ["/m/01JABC", "detail"],
    ["/", "home"],
  ])("keeps %s out of the root handle route (%s)", (pathname, id) => {
    expect(resolveRoute(pathname).id).toBe(id);
    expect(resolveRoute(pathname).id).not.toBe("profile");
  });
});

describe("profile feed addresses", () => {
  it.each([
    ["/owner", "all"],
    ["/owner/", "all"],
    ["/owner/all", "all"],
    ["/owner/posts", "moment"],
    ["/owner/articles/", "article"],
  ])("reads the filter from %s", (pathname, filter) => {
    expect(profileFromPath(pathname)).toEqual({ username: "owner", filter });
  });

  it("keeps the account alias and canonical feed addresses distinct", () => {
    expect(profileFromPath("/profile")).toEqual({
      username: null,
      filter: "all",
    });
    expect(profileFeedPath("owner", "all")).toBe("/owner/all");
    expect(profileFeedPath("owner", "moment")).toBe("/owner/posts");
    expect(profileFeedPath("owner", "article")).toBe("/owner/articles");
    expect(profileFromPath("/owner/article/id")).toBeNull();
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
    expect(resolveRoute(href).activeAccountOnly).not.toBe(true);
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
    expect(ARTICLE_COMPOSE_PATH).toBe("/compose/articles");
    expect(resolveRoute(ARTICLE_COMPOSE_PATH).id).toBe("article-workspace");
    expect(resolveRoute("/compose/article/").id).toBe("article-workspace");
  });

  it("carries the draft being edited in the address", () => {
    expect(resolveRoute("/compose/article/01K2ABC").id).toBe(
      "article-workspace",
    );
    expect(articleDraftIdFromPath("/compose/article/01K2ABC")).toBe("01K2ABC");
    expect(articleDraftIdFromPath("/compose/article/01K2ABC/")).toBe("01K2ABC");
  });

  it("means a new article when it names no draft", () => {
    expect(articleDraftIdFromPath(ARTICLE_COMPOSE_PATH)).toBeNull();
    expect(articleDraftIdFromPath("/compose")).toBeNull();
  });

  it("builds the address for a draft with the id escaped", () => {
    expect(articleDraftPath("01K2/ABC")).toBe(
      "/compose/articles/drafts/01K2%2FABC",
    );
  });

  it("does not take the moment composer's addresses with it", () => {
    expect(resolveRoute(COMPOSE_PATH).id).toBe("compose");
    expect(resolveRoute("/compose/drafts").id).toBe("compose");
    expect(isComposePath(ARTICLE_COMPOSE_PATH)).toBe(false);
  });

  it("uses account permissions rather than an owner-only route", () => {
    expect(
      resolveRoute(ARTICLE_COMPOSE_PATH).activeAccountOnly,
    ).toBeUndefined();
  });
});

describe("article workspace resource boundaries", () => {
  it.each([
    "/compose/articles",
    "/compose/articles/published",
    "/compose/articles/published/01K2ABC",
    "/compose/articles/drafts/01K2ABC",
    "/compose/articles/drafts/01K2ABC/focus",
  ])("resolves %s independently of site ownership", (path) => {
    expect(resolveRoute(path).id).toBe("article-workspace");
    expect(resolveRoute(path).activeAccountOnly).toBeUndefined();
  });
  it("keeps the draft identity when entering focus", () => {
    expect(
      articleDraftIdFromPath("/compose/articles/drafts/01K2ABC/focus"),
    ).toBe("01K2ABC");
  });
});

describe("retired identity routes", () => {
  it.each([
    "/owner/compose",
    "/owner/compose/drafts",
    "/owner/settings",
    "/owner/m/id/edit",
  ])("does not expose %s", (pathname) => {
    expect(resolveRoute(pathname).id).toBe("not-found");
  });
});

describe("central session requirements", () => {
  it.each([
    "/settings",
    "/settings/account/password",
    "/settings/security/tokens",
    "/settings/display/language",
    "/settings/site/comments",
    "/settings/members/registration",
    "/settings/backups/restore",
    "/settings/account",
    "/settings/site",
    "/settings/members",
    "/settings/backups",
    "/settings/profile",
    "/compose",
    "/compose/drafts",
    "/compose/articles/drafts/item",
    "/m/item/edit",
    "/profile/",
    "/archive",
  ])("requires a session for %s", (path) => {
    expect(resolveRoute(path).requiresSession).toBe(true);
  });
  it.each([
    "/",
    "/login",
    "/register",
    "/writer",
    "/writer/articles",
    "/m/item",
    "/m/item/comments/reply",
    "/search",
  ])("keeps %s public", (path) => {
    expect(resolveRoute(path).requiresSession).toBe(false);
  });
});
