import type { SVGProps } from "react";

/**
 * The Moment glyph on its own, drawn in `currentColor`.
 *
 * `public/moment-mark.svg` is the installable icon: a white plate with the
 * glyph struck through it, which is what a home screen and a browser tab need.
 * Inside a page that plate is wrong in both directions -- invisible on a light
 * canvas, a bright rectangle on a dark one -- and the file itself is pinned by
 * the visual contract, so the glyph is drawn here instead and takes the colour
 * of whatever it sits on.
 */
export function MomentMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      focusable="false"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={30}
      viewBox="0 0 512 512"
      {...props}
    >
      <path d="M128 370V146l128 132 128-132v224" />
    </svg>
  );
}
