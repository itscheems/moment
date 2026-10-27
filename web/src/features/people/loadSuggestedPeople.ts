import {
  PublicApiError,
  type PublicAccount,
  type PublicApi,
} from "../../api/public";

export async function loadSuggestedPeople(
  api: Pick<PublicApi, "user">,
  usernames: readonly string[],
  exclusions: { userId?: string | null; username?: string | null },
  signal?: AbortSignal,
): Promise<PublicAccount[]> {
  signal?.throwIfAborted();
  const people = new Map<string, PublicAccount>();
  for (const username of new Set(usernames)) {
    signal?.throwIfAborted();
    if (username === exclusions.username) continue;
    try {
      const person = await api.user(username, signal);
      if (
        person.userId !== exclusions.userId &&
        person.username !== exclusions.username &&
        !people.has(person.userId)
      )
        people.set(person.userId, person);
    } catch (error) {
      if (!(error instanceof PublicApiError && error.status === 404))
        throw error;
    }
  }
  signal?.throwIfAborted();
  return [...people.values()];
}
