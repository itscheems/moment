import { describe, expect, it } from "vitest";

import { COMPOSE_PATH, movedComposePath, resolveRoute } from "./routes";

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
