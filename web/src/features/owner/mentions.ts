export interface ActiveMention {
  start: number;
  end: number;
  query: string;
}

export interface MentionSegment {
  text: string;
  mention: boolean;
}

export function activeMention(
  value: string,
  caret: number,
): ActiveMention | null {
  const prefix = value.slice(0, caret);
  const match = prefix.match(/(?:^|[\s([{，。！？：；])@([a-zA-Z0-9_]*)$/);
  if (!match) return null;
  return {
    start: caret - match[1].length - 1,
    end: caret,
    query: match[1],
  };
}

export function mentionSegments(
  value: string,
  username: string,
): MentionSegment[] {
  const normalized = username.trim();
  if (!value || !normalized) return [{ text: value, mention: false }];

  const escaped = normalized.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const pattern = new RegExp(
    `(^|[^A-Za-z0-9_])(@${escaped})(?![A-Za-z0-9_])`,
    "gi",
  );
  const segments: MentionSegment[] = [];
  let cursor = 0;

  for (const match of value.matchAll(pattern)) {
    const index = match.index ?? 0;
    const boundary = match[1] ?? "";
    const mention = match[2] ?? "";
    const mentionStart = index + boundary.length;
    if (mentionStart > cursor) {
      segments.push({
        text: value.slice(cursor, mentionStart),
        mention: false,
      });
    }
    segments.push({ text: mention, mention: true });
    cursor = index + match[0].length;
  }

  if (cursor < value.length) {
    segments.push({ text: value.slice(cursor), mention: false });
  }
  return segments.length > 0 ? segments : [{ text: value, mention: false }];
}
