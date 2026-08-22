import { describe, expect, it } from "vitest";

import { resolveRoute } from "./routes";

describe("route boundaries", () => {
  it.each([
    ["/", "home"],
    ["/m/01JABC", "detail"],
    ["/search", "search"],
    ["/archive", "archive"],
    ["/owner/login", "owner-login"],
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
    ["/owner/login", "owner-login"],
    ["/owner/settings", "owner-settings"],
    ["/owner/compose", "owner-compose"],
    ["/m/01JABC", "detail"],
    ["/", "home"],
  ])("keeps %s out of the root handle route (%s)", (pathname, id) => {
    expect(resolveRoute(pathname).id).toBe(id);
    expect(resolveRoute(pathname).id).not.toBe("profile");
  });
});
