import { useTranslation } from "react-i18next";
import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

import type { PublicApi } from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { HeaderActions } from "../../components/headerActions";
import { Icon } from "../../components/icons";
import { useSession } from "../auth/SessionProvider";
import {
  listenForPostChanges,
  listenForPublishedPosts,
} from "../posts/postEvents";
import styles from "./ProfilePage.module.css";

export interface ProfileHeaderIdentity {
  userId: string;
  title: string;
  postCount: number | null;
}

export const ProfileHeaderContext = createContext<Dispatch<
  SetStateAction<ProfileHeaderIdentity | null>
> | null>(null);

export function ProfileHeader({
  api,
  userId,
  title,
}: {
  api: PublicApi;
  userId: string;
  title: string;
}) {
  const { t } = useTranslation();

  const setHeader = useContext(ProfileHeaderContext);
  const { account } = useSession();
  const [postCount, setPostCount] = useState<number | null>(null);

  useEffect(() => {
    if (!setHeader) return;
    setPostCount(null);
    let controller: AbortController | null = null;
    const refresh = () => {
      controller?.abort();
      controller = new AbortController();
      const signal = controller.signal;
      void api
        .timeline({ authorUserId: userId, limit: 1, signal })
        .then((page) => {
          if (!signal.aborted) setPostCount(page.authorPostCount ?? null);
        })
        .catch(() => {
          // A failed count must not hide the profile; focus retries the read.
        });
    };
    refresh();
    const stopChanges = listenForPostChanges((event) => {
      if (event.kind === "created" || event.kind === "deleted") refresh();
    });
    const stopPublished = listenForPublishedPosts(refresh);
    window.addEventListener("focus", refresh);
    return () => {
      controller?.abort();
      stopChanges();
      stopPublished();
      window.removeEventListener("focus", refresh);
    };
  }, [api, userId, account?.userId, setHeader]);

  useLayoutEffect(() => {
    setHeader?.({ userId, title, postCount });
    return () => setHeader?.(null);
  }, [setHeader, userId, title, postCount]);

  return (
    <HeaderActions>
      <InternalLink
        aria-label={t("搜索 Moment")}
        className={styles.headerSearch}
        href="/search"
        title={t("搜索 Moment")}
      >
        <Icon name="search" variant="navigation" />
      </InternalLink>
    </HeaderActions>
  );
}
