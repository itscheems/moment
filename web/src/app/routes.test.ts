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
    ["/missing", "not-found"],
  ])("maps %s to %s", (pathname, id) => {
    expect(resolveRoute(pathname).id).toBe(id);
  });
});
