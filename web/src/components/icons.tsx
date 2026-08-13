import type { SVGProps } from "react";

export type IconName =
  | "home"
  | "search"
  | "archive"
  | "compose"
  | "publish"
  | "owner"
  | "settings"
  | "theme"
  | "back"
  | "reply"
  | "share"
  | "media"
  | "more"
  | "edit"
  | "delete"
  | "pin"
  | "link"
  | "open"
  | "close"
  | "reset"
  | "camera"
  | "location";

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
  compose: (
    <>
      <path d="m5 19 1.2-5.1L16.8 3.3l3.9 3.9L10.1 17.8z" />
      <path d="m14.8 5.3 3.9 3.9M5 19l5.1-1.2M4 21h16" />
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
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.8 1.8 0 0 0 .36 2l.06.06-2.76 2.76-.06-.06a1.8 1.8 0 0 0-2-.36 1.8 1.8 0 0 0-1.1 1.64V21H10v-.08A1.8 1.8 0 0 0 8.9 19.3a1.8 1.8 0 0 0-2 .36l-.06.06-2.76-2.76.06-.06a1.8 1.8 0 0 0 .36-2A1.8 1.8 0 0 0 2.86 14H2v-4h.86A1.8 1.8 0 0 0 4.5 8.9a1.8 1.8 0 0 0-.36-2l-.06-.06 2.76-2.76.06.06a1.8 1.8 0 0 0 2 .36A1.8 1.8 0 0 0 10 2.86V2h4v.86a1.8 1.8 0 0 0 1.1 1.64 1.8 1.8 0 0 0 2-.36l.06-.06 2.76 2.76-.06.06a1.8 1.8 0 0 0-.36 2A1.8 1.8 0 0 0 21.14 10H22v4h-.86A1.8 1.8 0 0 0 19.4 15Z" />
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
    <path d="M21 11.75c0 4.28-4.03 7.75-9 7.75-1.08 0-2.12-.16-3.08-.45L4 20l1.18-3.86A7.12 7.12 0 0 1 3 11.75C3 7.47 7.03 4 12 4s9 3.47 9 7.75Z" />
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
  pin: (
    <>
      <path d="M8 4.5h8M9 4.5v5l-2.5 3h11L15 9.5v-5" />
      <path d="M12 12.5V21" />
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
  close: <path d="m6 6 12 12M18 6 6 18" />,
  reset: (
    <>
      <path d="M5.2 8.5A8 8 0 1 1 4 15" />
      <path d="M5.2 4.5v4h4" />
    </>
  ),
  camera: (
    <>
      <path d="M4 8.5h3l1.5-2h7l1.5 2h3v10H4z" />
      <circle cx="12" cy="13.5" r="3" />
    </>
  ),
  location: (
    <>
      <path d="M19 10.2c0 5-7 10.3-7 10.3S5 15.2 5 10.2a7 7 0 1 1 14 0Z" />
      <circle cx="12" cy="10" r="2.4" />
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
      data-icon={name}
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
