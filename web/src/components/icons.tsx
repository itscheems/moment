import type { SVGProps } from "react";

const iconSprite = "/icons/ui-symbols-v2.svg";

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
  | "media-picker"
  | "more"
  | "edit"
  | "delete"
  | "pin"
  | "link"
  | "open"
  | "close"
  | "reset"
  | "camera"
  | "location"
  | "location-picker";

export type IconVariant = "default" | "navigation" | "navigation-active";

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
    <path d="M12 20.25c4.97 0 9-3.694 9-8.25s-4.03-8.25-9-8.25S3 7.444 3 12c0 2.104.859 4.023 2.273 5.48.432.447.74 1.04.586 1.641a4.483 4.483 0 0 1-.923 1.785A5.969 5.969 0 0 0 6 21c1.282 0 2.47-.402 3.445-1.087.81.22 1.668.337 2.555.337Z" />
  ),
  share: (
    <>
      <path d="M12 16V4M7.5 8.5 12 4l4.5 4.5" />
      <path d="M5 13.5V20h14v-6.5" />
    </>
  ),
  media: (
    <path d="m2.25 15.75 5.159-5.159a2.25 2.25 0 0 1 3.182 0l5.159 5.159m-1.5-1.5 1.409-1.409a2.25 2.25 0 0 1 3.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 0 0 1.5-1.5V6a1.5 1.5 0 0 0-1.5-1.5H3.75A1.5 1.5 0 0 0 2.25 6v12a1.5 1.5 0 0 0 1.5 1.5Zm10.5-11.25h.008v.008h-.008V8.25Z" />
  ),
  "media-picker": (
    <>
      <path d="M15 7c1.105 0 2 .895 2 2s-.895 2-2 2-2-.895-2-2 .895-2 2-2z" />
      <path
        clipRule="evenodd"
        d="M14.1 2.5c1.103 0 1.991-.001 2.709.058.728.06 1.368.185 1.96.487.941.48 1.707 1.245 2.186 2.185.302.593.428 1.233.487 1.961.059.718.058 1.606.058 2.71V14.1c0 1.103.001 1.991-.058 2.709-.06.728-.185 1.368-.487 1.96-.48.941-1.245 1.707-2.185 2.186-.593.302-1.233.428-1.961.487-.718.059-1.606.058-2.71.058H9.9c-1.103 0-1.991.001-2.709-.058-.728-.06-1.368-.185-1.96-.487-.941-.48-1.707-1.245-2.186-2.185-.302-.593-.428-1.233-.487-1.961-.059-.718-.058-1.606-.058-2.71V9.9c0-1.103-.001-1.991.058-2.709.06-.728.185-1.368.487-1.96.48-.941 1.245-1.707 2.185-2.186.593-.302 1.233-.428 1.961-.487.718-.059 1.606-.058 2.71-.058H14.1zM4.506 15.442c.006.473.017.865.045 1.203.05.606.142.954.276 1.217.288.565.746 1.023 1.31 1.31.264.135.612.228 1.217.277.617.05 1.41.051 2.546.051h4.2c.36 0 .685-.003.981-.005L7.96 12.373l-3.453 3.07zM9.9 4.5c-1.136 0-1.929 0-2.546.05-.605.05-.953.143-1.216.277-.565.288-1.023.746-1.31 1.31-.135.264-.228.612-.277 1.217C4.5 7.971 4.5 8.764 4.5 9.9v2.873l3.54-3.147 9.63 9.63c.07-.026.133-.053.192-.083.565-.288 1.023-.746 1.31-1.31.135-.264.228-.612.277-1.218.05-.616.051-1.409.051-2.545V9.9c0-1.136 0-1.929-.05-2.546-.05-.605-.143-.953-.277-1.216-.288-.565-.746-1.023-1.31-1.31-.264-.135-.612-.228-1.218-.277C16.03 4.5 15.236 4.5 14.1 4.5H9.9z"
        fillRule="evenodd"
      />
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
  delete: <use href={`${iconSprite}#trash`} />,
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
  camera: <use href={`${iconSprite}#profile-camera`} />,
  location: (
    <>
      <path d="M19 10.2c0 5-7 10.3-7 10.3S5 15.2 5 10.2a7 7 0 1 1 14 0Z" />
      <circle cx="12" cy="10" r="2.4" />
    </>
  ),
  "location-picker": (
    <path d="M12 7c-1.93 0-3.5 1.57-3.5 3.5S10.07 14 12 14s3.5-1.57 3.5-3.5S13.93 7 12 7zm0 5c-.827 0-1.5-.673-1.5-1.5S11.173 9 12 9s1.5.673 1.5 1.5S12.827 12 12 12zm0-10c-4.687 0-8.5 3.813-8.5 8.5 0 5.967 7.621 11.116 7.945 11.332l.555.37.555-.37c.324-.216 7.945-5.365 7.945-11.332C20.5 5.813 16.687 2 12 2zm0 17.77c-1.665-1.241-6.5-5.196-6.5-9.27C5.5 6.916 8.416 4 12 4s6.5 2.916 6.5 6.5c0 4.073-4.835 8.028-6.5 9.27z" />
  ),
};

const filledIcons = new Set<IconName>([
  "delete",
  "camera",
  "media-picker",
  "location-picker",
]);

const navigationSymbols: Partial<
  Record<IconName, { idle: string; active: string }>
> = {
  home: { idle: "nav-home", active: "nav-home-active" },
  search: { idle: "nav-search", active: "nav-search-active" },
  archive: { idle: "nav-archive", active: "nav-archive-active" },
  owner: { idle: "nav-profile", active: "nav-profile-active" },
  more: { idle: "nav-more", active: "nav-more-active" },
};

export function Icon({
  name,
  variant = "default",
  ...props
}: {
  name: IconName;
  variant?: IconVariant;
} & SVGProps<SVGSVGElement>) {
  const navigationSymbol =
    variant === "default"
      ? undefined
      : navigationSymbols[name]?.[
          variant === "navigation-active" ? "active" : "idle"
        ];
  const filled = navigationSymbol !== undefined || filledIcons.has(name);
  const artwork = navigationSymbol ? (
    <use href={`${iconSprite}#${navigationSymbol}`} />
  ) : (
    paths[name]
  );
  return (
    <svg
      aria-hidden="true"
      data-icon={name}
      fill={filled ? "currentColor" : "none"}
      height="24"
      viewBox="0 0 24 24"
      width="24"
      {...props}
    >
      {filled ? (
        artwork
      ) : (
        <g
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="1.7"
        >
          {artwork}
        </g>
      )}
    </svg>
  );
}
