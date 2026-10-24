import type { useSession } from "../auth/SessionProvider";
import { useEffect, useState } from "react";

type SessionState = ReturnType<typeof useSession>["state"];

/** Preserve buffer ownership while identity is checked; this grants no authority. */
export function useDiscussionIdentity(
  accountId: string | null,
  state: SessionState,
) {
  const [lastVerifiedAccountId, setLastVerifiedAccountId] = useState(accountId);
  useEffect(() => {
    if (state === "guest") setLastVerifiedAccountId(null);
    else if (accountId !== null) setLastVerifiedAccountId(accountId);
  }, [accountId, state]);
  return {
    accountKey: state === "guest" ? null : (accountId ?? lastVerifiedAccountId),
    confirming:
      state !== "guest" &&
      accountId === null &&
      (lastVerifiedAccountId !== null || state === "signed-in"),
  };
}
