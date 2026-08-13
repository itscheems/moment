export interface ActiveMention {
  start: number;
  end: number;
  query: string;
}

export interface MentionSegment {
  text: string;
  mention: boolean;
}

export interface MentionPanelGeometry {
  anchorLeft: number;
  anchorTop: number;
  editorLeft: number;
  editorTop: number;
  editorWidth: number;
  itemCount: number;
  lineHeight: number;
  scrollLeft: number;
  scrollTop: number;
  viewportHeight: number;
}

export function mentionPanelPosition({
  anchorLeft,
  anchorTop,
  editorLeft,
  editorTop,
  editorWidth,
  itemCount,
  lineHeight,
  scrollLeft,
  scrollTop,
  viewportHeight,
}: MentionPanelGeometry) {
  const width = Math.min(352, editorWidth);
  const left = Math.max(
    0,
    Math.min(anchorLeft - editorLeft - scrollLeft, editorWidth - width),
  );
  const visibleAnchorTop = anchorTop - scrollTop;
  const below = viewportHeight - visibleAnchorTop - lineHeight - 6;
  const panelHeight = Math.min(
    itemCount * 68 + 2,
    Math.min(288, viewportHeight * 0.42),
  );
  const placeAbove = below < panelHeight && visibleAnchorTop - 6 > below;
  const top = Math.max(
    0,
    anchorTop -
      editorTop -
      scrollTop +
      (placeAbove ? -panelHeight - 6 : lineHeight + 6),
  );
  return { left, top, width };
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
