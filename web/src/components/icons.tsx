import type { SVGProps } from "react";

export type IconName =
  | "home"
  | "search"
  | "archive"
  | "publish"
  | "owner"
  | "theme"
  | "back"
  | "reply"
  | "share"
  | "media"
  | "more"
  | "edit"
  | "delete"
  | "link"
  | "open";

const paths: Record<IconName, React.ReactNode> = {
  home: (
    <path d="M4 11.5 12 5l8 6.5V20a1 1 0 0 1-1 1h-4.5v-6h-5v6H5a1 1 0 0 1-1-1v-8.5Z" />
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4.25 4.25" />
    </>
  ),
  archive: (
    <>
      <path d="M5 4.5h14v15H5z" />
      <path d="M8 2.5v4M16 2.5v4M5 9h14M8.5 13h2M13.5 13h2M8.5 16.5h2" />
    </>
  ),
  publish: (
    <>
      <path d="M12 19V5M6.5 10.5 12 5l5.5 5.5" />
      <path d="M5 19h14" />
    </>
  ),
  owner: (
    <>
      <circle cx="12" cy="8.5" r="3.5" />
      <path d="M5.5 20c.7-3.5 2.87-5.5 6.5-5.5s5.8 2 6.5 5.5" />
    </>
  ),
  theme: (
    <>
      <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.64 5.64l1.42 1.42M16.94 16.94l1.42 1.42M18.36 5.64l-1.42 1.42M7.06 16.94l-1.42 1.42" />
      <circle cx="12" cy="12" r="4" />
    </>
  ),
  back: (
    <>
      <path d="m14.5 6-6 6 6 6" />
      <path d="M9 12h10" />
    </>
  ),
  reply: (
    <path d="M20 11.5a7.5 7.5 0 0 1-8 7.45 8.7 8.7 0 0 1-3.2-.92L4 19l1.1-4.2A7.5 7.5 0 1 1 20 11.5Z" />
  ),
  share: (
    <>
      <path d="M12 16V4M7.5 8.5 12 4l4.5 4.5" />
      <path d="M5 13.5V20h14v-6.5" />
    </>
  ),
  media: (
    <>
      <rect height="15" rx="2" width="18" x="3" y="4.5" />
      <circle cx="8.25" cy="9" r="1.4" />
      <path d="m5.5 17 4.2-4.2 2.8 2.8 2.15-2.15L18.5 17" />
    </>
  ),
  more: (
    <>
      <circle cx="5" cy="12" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
    </>
  ),
  edit: (
    <>
      <path d="m14.5 5.5 4 4L9 19l-4.5.5L5 15z" />
      <path d="m12.5 7.5 4 4" />
    </>
  ),
  delete: (
    <>
      <path d="M5 7h14M9 7V4.5h6V7M7.5 7l.8 13h7.4l.8-13" />
      <path d="M10 10.5v6M14 10.5v6" />
    </>
  ),
  link: (
    <>
      <path d="M9.5 14.5 14.5 9.5" />
      <path
        d="M7.5 17.5 5.8 19.2a3.5 3.5 0 0 1-5-5l4-4a3.5 3.5 0 0 1 5 0"
        transform="translate(2 -2)"
      />
      <path
        d="m16.5 6.5 1.7-1.7a3.5 3.5 0 0 1 5 5l-4 4a3.5 3.5 0 0 1-5 0"
        transform="translate(-2 2)"
      />
    </>
  ),
  open: (
    <>
      <path d="M14 4h6v6M20 4l-9 9" />
      <path d="M19 14v5H5V5h5" />
    </>
  ),
};

export function Icon({
  name,
  ...props
}: { name: IconName } & SVGProps<SVGSVGElement>) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height="24"
      viewBox="0 0 24 24"
      width="24"
      {...props}
    >
      <g
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.7"
      >
        {paths[name]}
      </g>
    </svg>
  );
}
