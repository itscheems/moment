import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { Composer } from "./Composer";
import styles from "./Owner.module.css";

export function EditPage({
  api,
  id,
  site,
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
}) {
  const [post, setPost] = useState<PublicPost | null>(null);
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
      setPost(await api.post(id, controller.signal));
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
  }, [api, id]);

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
