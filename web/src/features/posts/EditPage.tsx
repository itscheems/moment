import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  isMomentPost,
  type PublicMomentPost,
  type SiteIdentity,
} from "../../api/public";
import { useSession } from "../auth/SessionProvider";
import { Composer } from "../compose/Composer";
import styles from "../../components/Forms.module.css";

export function EditPage({
  api,
  id,
  site,
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
}) {
  const { account } = useSession();
  const [post, setPost] = useState<PublicMomentPost | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const active = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setLoading(true);
    setError(null);
    try {
      const loaded = await api.post(id, controller.signal);
      // The composer edits Markdown and saves it back over `content_md`. An
      // article has none, so opening one here would offer an empty editor and
      // then write that emptiness over a body it never showed.
      if (!isMomentPost(loaded)) {
        setError("这是一篇文章，不能在这里编辑。");
        return;
      }
      if (!account || loaded.author?.userId !== account.userId) {
        setError("你只能编辑自己发布的 Moment。");
        return;
      }
      setPost(loaded);
    } catch (nextError) {
      if (controller.signal.aborted) return;
      setError(
        nextError instanceof PublicApiError && nextError.status === 404
          ? "这个 Moment 已不存在，无法编辑。"
          : "没有加载到要编辑的 Moment。",
      );
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, id, account?.userId]);

  useEffect(() => {
    void load();
    return () => active.current?.abort();
  }, [load]);

  if (loading) return <p className={styles.routeState}>正在加载正文…</p>;
  if (error) {
    return (
      <div className={styles.routeState} role="alert">
        <p>{error}</p>
        <button onClick={() => void load()} type="button">
          重试加载
        </button>
      </div>
    );
  }
  return post ? <Composer post={post} site={site} variant="edit" /> : null;
}
