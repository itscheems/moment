/** Sensitive browser data belongs to one account on one instance. */
export function accountCacheScope(
  instanceId: string | null,
  accountId: string | null,
): string | null {
  const id = /^[0-9a-f]{32}$/;
  return instanceId && accountId && id.test(instanceId) && id.test(accountId)
    ? `${instanceId}.${accountId}`
    : null;
}
