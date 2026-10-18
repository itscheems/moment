import { useEffect, useLayoutEffect, useRef, type MouseEvent } from "react";
import { useNavigation } from "../../app/navigation";

export const settingsSections = [
  "account",
  "site",
  "members",
  "backups",
] as const;
export type SettingsSection = (typeof settingsSections)[number];

export function settingsSection(pathname: string): SettingsSection {
  const requested = pathname.split("/")[2];
  return settingsSections.find((section) => section === requested) ?? "account";
}

function clickScrollBehavior(): ScrollBehavior {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
    ? "instant"
    : "smooth";
}

/** Keep one mounted form surface while the address follows the section in view. */
export function useSettingsSections(admin: boolean) {
  const { pathname, navigate, replaceWithinPage } = useNavigation();
  const section = settingsSection(pathname);
  const navRef = useRef<HTMLElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const scrollAddress = useRef<string | null>(null);
  const destination = useRef<HTMLElement | null>(null);
  const smoothNext = useRef(false);

  useLayoutEffect(() => {
    if (scrollAddress.current === pathname) {
      scrollAddress.current = null;
      return;
    }
    const target = document.getElementById(`settings-${section}`);
    destination.current = target;
    const behavior = smoothNext.current ? clickScrollBehavior() : "instant";
    smoothNext.current = false;
    // Navigation restores an entry in its own frame. Apply the section anchor
    // afterwards so an interrupted smooth scroll cannot restore halfway there.
    const frame = window.requestAnimationFrame(() => {
      target?.scrollIntoView?.({ block: "start", behavior });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [pathname, section, admin]);

  useEffect(() => {
    const nav = navRef.current;
    const link = nav?.querySelector<HTMLElement>(
      `a[href="/settings/${section}"]`,
    );
    if (nav && link && nav.scrollWidth > nav.clientWidth)
      nav.scrollTo({
        left: link.offsetLeft - (nav.clientWidth - link.offsetWidth) / 2,
        behavior: "instant",
      });
  }, [section]);

  useEffect(() => {
    let frame = 0;
    const sync = () => {
      if (destination.current) return;
      const nav = navRef.current?.getBoundingClientRect();
      // A hidden or detached surface has no viewport position to synchronize.
      if (!nav?.height) return;
      const sections = settingsSections.flatMap((id) => {
        const element = document.getElementById(`settings-${id}`);
        return element ? [{ id, element }] : [];
      });
      let current = sections[0]?.id;
      for (const { id, element } of sections) {
        if (element.getBoundingClientRect().top <= nav.bottom + 1) current = id;
      }
      if (!current) return;
      const address = `/settings/${current}`;
      if (window.location.pathname === address) return;
      scrollAddress.current = address;
      replaceWithinPage(address);
    };
    const schedule = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(sync);
    };
    const releaseDestination = () => {
      destination.current = null;
    };
    // Keep a deep link aligned while asynchronous form data changes its height,
    // until the reader takes control. This never repositions an edited form.
    const resize =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            if (destination.current)
              destination.current.scrollIntoView?.({
                block: "start",
                behavior: "instant",
              });
            else schedule();
          });
    if (contentRef.current) resize?.observe(contentRef.current);
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    window.addEventListener("wheel", releaseDestination, { passive: true });
    window.addEventListener("touchstart", releaseDestination, {
      passive: true,
    });
    window.addEventListener("pointerdown", releaseDestination, {
      passive: true,
    });
    window.addEventListener("keydown", releaseDestination);
    return () => {
      window.cancelAnimationFrame(frame);
      resize?.disconnect();
      window.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      window.removeEventListener("wheel", releaseDestination);
      window.removeEventListener("touchstart", releaseDestination);
      window.removeEventListener("pointerdown", releaseDestination);
      window.removeEventListener("keydown", releaseDestination);
    };
  }, [admin, replaceWithinPage]);

  const goToSection = (
    event: MouseEvent<HTMLAnchorElement>,
    next: SettingsSection,
  ) => {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.altKey ||
      event.shiftKey
    )
      return;
    event.preventDefault();
    if (next === section) {
      destination.current = document.getElementById(`settings-${next}`);
      document
        .getElementById(`settings-${next}`)
        ?.scrollIntoView?.({ block: "start", behavior: clickScrollBehavior() });
      return;
    }
    smoothNext.current = true;
    navigate(`/settings/${next}`, { preserveScroll: true });
  };

  return { section, navRef, contentRef, goToSection };
}
