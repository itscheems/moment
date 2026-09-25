import type { SVGProps } from "react";

/**
 * The editor's own glyphs.
 *
 * Deliberately not added to components/icons.tsx: that sprite is fetched by
 * every reader on their first page, and bold, quote and table marks are only
 * ever pressed by the one person who writes. Keeping them here keeps them in
 * the editor's chunk, which is the whole point of the boundary this directory
 * draws.
 */
export type EditorIconName =
  | "bold"
  | "italic"
  | "underline"
  | "strike"
  | "code"
  | "quote"
  | "bullet-list"
  | "ordered-list"
  | "table"
  | "image"
  | "rule"
  | "link"
  | "unlink"
  | "chevron";

const paths: Record<EditorIconName, React.ReactNode> = {
  bold: (
    <path
      d="M7 4h5.5a4 4 0 0 1 0 8H7zm0 8h6.2a4 4 0 0 1 0 8H7z"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinejoin="round"
    />
  ),
  italic: (
    <path
      d="M15 4h-5m4 0-4 16m4-16M9 20H4m5 0 4-16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
    />
  ),
  underline: (
    <path
      d="M6 4v7a6 6 0 0 0 12 0V4M5 20h14"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
    />
  ),
  strike: (
    <path
      d="M4 12h16M7.5 8a3.5 3.5 0 0 1 3.5-3h2.5a3.5 3.5 0 0 1 3.4 2.6M16.5 16a3.5 3.5 0 0 1-3.5 3h-2.6a3.5 3.5 0 0 1-3.4-2.6"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
    />
  ),
  code: (
    <path
      d="m9 7-5 5 5 5m6-10 5 5-5 5"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  quote: (
    <path
      d="M9 6H5a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1h3v1a3 3 0 0 1-3 3v2a5 5 0 0 0 5-5V7a1 1 0 0 0-1-1Zm10 0h-4a1 1 0 0 0-1 1v5a1 1 0 0 0 1 1h3v1a3 3 0 0 1-3 3v2a5 5 0 0 0 5-5V7a1 1 0 0 0-1-1Z"
      fill="currentColor"
    />
  ),
  "bullet-list": (
    <>
      <path
        d="M9 6h11M9 12h11M9 18h11"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <circle cx="4.5" cy="6" fill="currentColor" r="1.5" />
      <circle cx="4.5" cy="12" fill="currentColor" r="1.5" />
      <circle cx="4.5" cy="18" fill="currentColor" r="1.5" />
    </>
  ),
  "ordered-list": (
    <>
      <path
        d="M9 6h11M9 12h11M9 18h11"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
      <text fill="currentColor" fontSize="7" fontWeight="700" x="2" y="8.5">
        1
      </text>
      <text fill="currentColor" fontSize="7" fontWeight="700" x="2" y="14.5">
        2
      </text>
      <text fill="currentColor" fontSize="7" fontWeight="700" x="2" y="20.5">
        3
      </text>
    </>
  ),
  table: (
    <path
      d="M4 5h16v14H4zM4 10h16M4 15h16M10 5v14"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinejoin="round"
    />
  ),
  image: (
    <path
      d="M4 5h16v14H4zm2.5 10.5 3.2-3.6 2.6 2.7 2.6-3.1 3.6 4M9 9.5a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinejoin="round"
    />
  ),
  rule: (
    <path
      d="M4 12h16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
    />
  ),
  link: (
    <path
      d="M10.5 13.5a4 4 0 0 0 5.7 0l2.6-2.6a4 4 0 0 0-5.7-5.7l-1.3 1.3m-2.3 3.5a4 4 0 0 0-5.7 0l-2.6 2.6a4 4 0 0 0 5.7 5.7l1.3-1.3"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      transform="translate(1.5 0.5)"
    />
  ),
  unlink: (
    <path
      d="M9 15 5.8 18.2a4 4 0 0 1-5.6-5.7L3.3 9.4m11.4 5.2 3.1-3.1a4 4 0 0 0-5.6-5.7L9 9M3 3l18 18"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      transform="translate(1.5 0.5)"
    />
  ),
  chevron: (
    <path
      d="m6 9 6 6 6-6"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
};

/**
 * One glyph, never larger than the text beside it.
 *
 * The size is set here and not only by the container: an SVG carrying nothing
 * but a viewBox falls back to 300x150, so a container that forgets to size it
 * gets a glyph the height of a page rather than a glyph that looks slightly
 * wrong. A container that does size it still wins, because a stylesheet beats
 * a presentation attribute.
 */
export function EditorIcon({
  name,
  ...props
}: SVGProps<SVGSVGElement> & { name: EditorIconName }) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      height="1em"
      viewBox="0 0 24 24"
      width="1em"
      {...props}
    >
      {paths[name]}
    </svg>
  );
}
