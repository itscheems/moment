/** Keep sign-in and registration redirects on this origin. */
export function safeReturnPath(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
  if (/[\\\r\n]/.test(value)) return "/";
  try {
    const resolved = new URL(value, "https://moment.invalid");
    if (resolved.origin !== "https://moment.invalid") return "/";
  } catch {
    return "/";
  }
  return value;
}

export function authPageHref(
  path: "/login" | "/register",
  search: string,
): string {
  const requested = new URLSearchParams(search).get("return");
  return requested === null
    ? path
    : `${path}?return=${encodeURIComponent(safeReturnPath(requested))}`;
}
