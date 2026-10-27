import { describe, expect, it, vi } from "vitest";
import {
  PublicApiError,
  type PublicAccount,
  type PublicApi,
} from "../../api/public";
import { loadSuggestedPeople } from "./loadSuggestedPeople";

function profile(username: string, userId = username): PublicAccount {
  return {
    userId,
    username,
    displayName: "Shared display name",
    bio: `${username}'s public biography`,
    avatarMediaId: null,
    coverMediaId: null,
    profileUrl: `/${username}`,
  };
}

describe("loadSuggestedPeople", () => {
  it("preserves candidate order and deduplicates usernames and account identities", async () => {
    const amber = profile("amber");
    const birch = profile("birch");
    const user = vi.fn<PublicApi["user"]>();
    user
      .mockResolvedValueOnce(amber)
      .mockResolvedValueOnce(birch)
      .mockResolvedValueOnce(profile("cedar", amber.userId));
    const signal = new AbortController().signal;

    const people = await loadSuggestedPeople(
      { user },
      ["amber", "birch", "amber", "cedar"],
      {},
      signal,
    );

    expect(people).toEqual([amber, birch]);
    expect(user.mock.calls).toEqual([
      ["amber", signal],
      ["birch", signal],
      ["cedar", signal],
    ]);
  });

  it("excludes the signed-in account by ID and the viewed profile by username", async () => {
    const user = vi.fn<PublicApi["user"]>();
    const cedar = profile("cedar");
    user
      .mockResolvedValueOnce(profile("amber", "viewer-id"))
      .mockResolvedValueOnce(cedar);

    expect(
      await loadSuggestedPeople({ user }, ["amber", "birch", "cedar"], {
        userId: "viewer-id",
        username: "birch",
      }),
    ).toEqual([cedar]);
    expect(user.mock.calls.map(([username]) => username)).toEqual([
      "amber",
      "cedar",
    ]);
  });

  it("also excludes a viewed profile returned under a different candidate handle", async () => {
    const user = vi.fn<PublicApi["user"]>().mockResolvedValue(profile("birch"));

    expect(
      await loadSuggestedPeople({ user }, ["amber"], { username: "birch" }),
    ).toEqual([]);
  });

  it("skips missing profiles and propagates other failures for retry", async () => {
    const user = vi.fn<PublicApi["user"]>();
    const birch = profile("birch");
    user
      .mockRejectedValueOnce(
        new PublicApiError("Missing account", {
          status: 404,
          code: "ACCOUNT_NOT_FOUND",
        }),
      )
      .mockResolvedValueOnce(birch);
    expect(await loadSuggestedPeople({ user }, ["amber", "birch"], {})).toEqual(
      [birch],
    );

    const error = new PublicApiError("Service unavailable", {
      status: 503,
      code: "UNAVAILABLE",
    });
    user.mockRejectedValueOnce(error);
    await expect(loadSuggestedPeople({ user }, ["cedar"], {})).rejects.toBe(
      error,
    );
  });

  it("returns no suggestions for an empty list without reading profiles", async () => {
    const user = vi.fn<PublicApi["user"]>();
    expect(await loadSuggestedPeople({ user }, [], {})).toEqual([]);
    expect(user).not.toHaveBeenCalled();
  });

  it("rejects cancelled loads before requests and before returning a late result", async () => {
    const controller = new AbortController();
    controller.abort();
    const user = vi.fn<PublicApi["user"]>();
    await expect(
      loadSuggestedPeople({ user }, ["amber"], {}, controller.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(user).not.toHaveBeenCalled();

    const lateController = new AbortController();
    user.mockImplementationOnce(async () => {
      lateController.abort();
      return profile("amber");
    });
    await expect(
      loadSuggestedPeople({ user }, ["amber"], {}, lateController.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
  });
});
