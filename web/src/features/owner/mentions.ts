export interface ActiveMention {
  start: number;
  end: number;
  query: string;
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
