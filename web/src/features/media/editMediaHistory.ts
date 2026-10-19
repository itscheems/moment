import { releaseLocalPreview, type ComposerMediaItem } from "./mediaState";

/** Keep upload results and owned previews alive while an edit can be undone. */
export class EditMediaHistory {
  private revisions = new WeakMap<ComposerMediaItem, number>();
  private resources = new Map<number, ComposerMediaItem>();
  private nextRevision = 0;
  private urls = new Set<string>();

  private revision(item: ComposerMediaItem): number {
    let revision = this.revisions.get(item);
    if (revision === undefined) {
      revision = ++this.nextRevision;
      this.revisions.set(item, revision);
    }
    return revision;
  }

  remember(items: ComposerMediaItem[], previous: ComposerMediaItem[] = []) {
    for (const item of items) {
      if (!this.revisions.has(item)) {
        const before = previous.find((candidate) => candidate.key === item.key);
        // applyUpload releases the File, but this is still the same edit.
        if (before && (!item.file || item.file === before.file))
          this.revisions.set(item, this.revision(before));
      }
      this.resources.set(this.revision(item), item);
      for (const url of [
        item.localPreviewUrl,
        item.processingPosterUrl,
        item.cropSource?.url,
      ]) {
        if (url?.startsWith("blob:")) this.urls.add(url);
      }
    }
  }

  restore(items: ComposerMediaItem[]) {
    return items.map((item) => {
      const revision = this.revision(item);
      const restored = {
        ...(this.resources.get(revision) ?? item),
        altMode: item.altMode,
        altText: item.altText,
        cropSource: item.cropSource,
        cropSelection: item.cropSelection,
      };
      this.revisions.set(restored, revision);
      return restored;
    });
  }

  clear() {
    for (const url of this.urls) releaseLocalPreview(url);
    this.urls.clear();
    this.resources.clear();
    this.revisions = new WeakMap();
  }
  signature(items: ComposerMediaItem[]) {
    return items.map((item) => [
      item.key,
      this.revision(item),
      item.cropSelection,
      item.altMode,
      item.altText,
    ]);
  }
}
