import { Dialog } from "@base-ui/react/dialog";
import { Tabs } from "@base-ui/react/tabs";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { mediaPosterUrl } from "../../api/mediaUrls";
import { COMPOSE_MEDIA_PATH } from "../../app/routes";
import { useOptionalNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  isVideoMimeType,
  MAX_ALT_CODE_POINTS,
  fileValidation,
  newMediaItem,
  type ComposerMediaItem,
} from "./mediaState";
import {
  canCropMedia,
  createMediaCrop,
  type MediaCropSelection,
  type MediaCropSource,
} from "./mediaCrop";
import { MediaCropPanel } from "./MediaCropPanel";
import styles from "./MediaDescriptionEditor.module.css";

function sourceOf(item: ComposerMediaItem): MediaCropSource {
  return (
    item.cropSource ?? {
      url: item.localPreviewUrl || item.originalUrl || item.previewUrl,
      name: item.file?.name || "image",
      mimeType: item.mimeType,
    }
  );
}
function CropPreview({
  source,
  selection,
  alt,
}: {
  source: MediaCropSource;
  selection: MediaCropSelection;
  alt: string;
}) {
  const clipId = useId();
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const { area, percentages } = selection;
  if (failedSource === source.url)
    return <span className={styles.pending}>Unable to preview this crop.</span>;
  // Preview the existing decoded source. File encoding belongs to Save, not
  // to tab changes or paging through images.
  return (
    <svg
      aria-label={alt}
      className={styles.cropPreview}
      preserveAspectRatio="xMidYMid meet"
      role="img"
      viewBox={`${area.x} ${area.y} ${area.width} ${area.height}`}
    >
      <defs>
        <clipPath id={clipId} clipPathUnits="userSpaceOnUse">
          <rect x={area.x} y={area.y} width={area.width} height={area.height} />
        </clipPath>
      </defs>
      <image
        clipPath={`url(#${clipId})`}
        height={(area.height * 100) / percentages.height}
        href={source.url}
        onError={() => setFailedSource(source.url)}
        preserveAspectRatio="none"
        width={(area.width * 100) / percentages.width}
      />
    </svg>
  );
}

export function MediaDescriptionEditor({
  items,
  disabled,
  onChange,
  editRequest,
}: {
  items: ComposerMediaItem[];
  disabled: boolean;
  onChange: (items: ComposerMediaItem[], message: string) => void;
  editRequest?: { key: string; version: number } | null;
}) {
  const navigation = useOptionalNavigation();
  const itemsRef = useRef(items);
  itemsRef.current = items;
  const id = useId();
  const [localOpen, setLocalOpen] = useState(false);
  const open = navigation
    ? navigation.pathname === COMPOSE_MEDIA_PATH && navigation.panel?.key === id
    : localOpen;
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [tab, setTab] = useState<"crop" | "alt">("alt");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [crops, setCrops] = useState<Record<string, MediaCropSelection>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const popupRef = useRef<HTMLDivElement>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const openedBefore = useRef(false);
  const beginRequest = useRef<{
    tab: "crop" | "alt";
    key: string | null;
  } | null>(null);
  const firstKey = items[0]?.key ?? null;
  const openPanel = navigation?.openPanel;
  const begin = useCallback(
    (nextTab: "crop" | "alt", key: string | null) => {
      beginRequest.current = { tab: nextTab, key };
      setTab(nextTab);
      setSelectedKey(key);
      if (openPanel) openPanel(COMPOSE_MEDIA_PATH, id);
      else setLocalOpen(true);
    },
    [id, openPanel],
  );
  const requestedKey =
    editRequest && items.some((item) => item.key === editRequest.key)
      ? editRequest.key
      : null;
  useEffect(() => {
    if (editRequest && requestedKey) begin("crop", requestedKey);
  }, [editRequest, requestedKey, begin]);
  useEffect(() => {
    if (open && !openedBefore.current) {
      const request = beginRequest.current;
      beginRequest.current = null;
      setDrafts({});
      setCrops({});
      setError(null);
      setSelectedKey(request?.key ?? firstKey);
      setTab(request?.tab ?? "alt");
    }
    if (!open) controllerRef.current?.abort();
    openedBefore.current = open;
  }, [open, firstKey]);
  useEffect(() => () => controllerRef.current?.abort(), []);
  const selectedIndex = Math.max(
    0,
    items.findIndex((item) => item.key === selectedKey),
  );
  const item = items[selectedIndex];
  const describing = open && Boolean(item);
  useDocumentScrollLock(describing);
  const locked = disabled || saving;
  const video = item && isVideoMimeType(item.mimeType);
  const cropAllowed = item && canCropMedia(item);
  const currentTab = cropAllowed ? tab : "alt";
  const text = item
    ? (drafts[item.key] ?? (item.altMode === "provided" ? item.altText : ""))
    : "";
  const count = items.filter(
    (candidate) => candidate.altMode === "provided",
  ).length;
  const preview = item
    ? video
      ? item.processingPosterUrl ||
        (item.mediaId ? mediaPosterUrl(item.mediaId) : null)
      : item.localPreviewUrl || item.previewUrl
    : null;
  const close = () => {
    if (navigation) navigation.closePanel(id);
    else setLocalOpen(false);
  };
  const changeOpen = (next: boolean) => {
    if (locked) return;
    if (next) begin("alt", firstKey);
    else close();
  };
  const save = async () => {
    if (locked) return;
    const controller = new AbortController();
    controllerRef.current = controller;
    setSaving(true);
    setError(null);
    try {
      const files = new Map<string, File>();
      for (const candidate of items) {
        const selection = crops[candidate.key];
        if (selection)
          files.set(
            candidate.key,
            await createMediaCrop(
              sourceOf(candidate),
              selection.area,
              controller.signal,
            ),
          );
      }
      const currentItems = itemsRef.current;
      for (const [key, file] of files) {
        const validation = fileValidation(
          file,
          currentItems
            .filter((candidate) => candidate.key !== key)
            .map((candidate) => ({
              file: files.get(candidate.key) ?? candidate.file,
            })),
        );
        if (validation) throw new Error(validation);
      }
      if (controller.signal.aborted) return;
      let changed = false;
      const next = currentItems.map((candidate) => {
        let value = candidate;
        const file = files.get(candidate.key);
        if (file) {
          changed = true;
          value = {
            ...newMediaItem(file),
            key: candidate.key,
            altText: candidate.altText,
            altMode: candidate.altMode,
            localPreviewUrl: URL.createObjectURL(file),
            cropSource: sourceOf(candidate),
            cropSelection: crops[candidate.key],
          };
        }
        const draft = drafts[candidate.key];
        if (draft === undefined) return value;
        const altText = draft.trim(),
          altMode = altText ? "provided" : "missing";
        if (value.altMode === altMode && value.altText === altText)
          return value;
        changed = true;
        return {
          ...value,
          altText,
          altMode: altText ? ("provided" as const) : ("missing" as const),
        };
      });
      if (changed) onChange(next, "媒体编辑已保存。");
      close();
    } catch (e) {
      if (!controller.signal.aborted)
        setError(
          e instanceof Error
            ? e.message
            : "Unable to save this image. Please try again.",
        );
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
        setSaving(false);
      }
    }
  };
  return (
    <Dialog.Root open={describing} onOpenChange={changeOpen}>
      <Dialog.Trigger
        className={styles.trigger}
        disabled={disabled}
        data-focus-key={`media-descriptions:${id}`}
      >
        <Icon name="description" />
        <span>
          {count > 0
            ? `${count} ${count === 1 ? "image description" : "image descriptions"}`
            : "Add descriptions"}
        </span>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup
            className={styles.popup}
            initialFocus={() =>
              currentTab === "alt" ? inputRef.current : popupRef.current
            }
            ref={popupRef}
          >
            <header className={styles.header}>
              <Dialog.Close
                className={styles.back}
                aria-label="Back"
                disabled={locked}
              >
                <Icon name="arrow-left" />
              </Dialog.Close>
              <Dialog.Title>
                {currentTab === "crop"
                  ? "Crop media"
                  : `Edit ${video ? "media" : "image"} description`}
              </Dialog.Title>
              {items.length > 1 ? (
                <div className={styles.pager}>
                  <button
                    aria-label="Previous image"
                    disabled={locked || selectedIndex === 0}
                    onClick={() =>
                      setSelectedKey(items[selectedIndex - 1]?.key ?? null)
                    }
                    type="button"
                  >
                    <Icon name="arrow-left" />
                  </button>
                  <button
                    aria-label="Next image"
                    disabled={locked || selectedIndex === items.length - 1}
                    onClick={() =>
                      setSelectedKey(items[selectedIndex + 1]?.key ?? null)
                    }
                    type="button"
                  >
                    <Icon name="arrow-right" />
                  </button>
                </div>
              ) : null}
              <button
                className={styles.save}
                disabled={locked}
                onClick={() => void save()}
                type="button"
              >
                {saving ? "Saving…" : "Save"}
              </button>
            </header>
            <Tabs.Root
              value={currentTab}
              onValueChange={(value) => setTab(value as "crop" | "alt")}
              className={styles.tabsRoot}
            >
              <Tabs.List className={styles.tabs} aria-label="Edit photo">
                <Tabs.Tab
                  value="crop"
                  aria-label="Crop media"
                  disabled={locked || !cropAllowed}
                >
                  <Icon name="crop" />
                </Tabs.Tab>
                <Tabs.Tab
                  value="alt"
                  aria-label="Edit image description"
                  disabled={locked}
                >
                  ALT
                </Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="crop" className={styles.tabPanel} keepMounted>
                {item && cropAllowed ? (
                  <MediaCropPanel
                    active={currentTab === "crop"}
                    key={item.key}
                    source={sourceOf(item).url}
                    initial={crops[item.key] ?? item.cropSelection}
                    disabled={locked}
                    onChange={(selection) =>
                      setCrops((current) => ({
                        ...current,
                        [item.key]: selection,
                      }))
                    }
                  />
                ) : null}
              </Tabs.Panel>
              <Tabs.Panel value="alt" className={styles.tabPanel} keepMounted>
                <div className={styles.stage}>
                  {item && crops[item.key] ? (
                    <CropPreview
                      source={sourceOf(item)}
                      selection={crops[item.key]}
                      alt={`Image ${selectedIndex + 1} of ${items.length}`}
                    />
                  ) : preview ? (
                    <img
                      alt={`Image ${selectedIndex + 1} of ${items.length}`}
                      src={preview}
                    />
                  ) : (
                    <span className={styles.pending}>Preparing media…</span>
                  )}
                </div>
                <div className={styles.description}>
                  <label className={styles.field}>
                    <span className={styles.label}>Description</span>
                    <span className={styles.counter}>
                      {Array.from(text).length} / 1,000
                    </span>
                    <textarea
                      aria-label="Description"
                      disabled={locked}
                      ref={inputRef}
                      rows={2}
                      value={text}
                      onChange={(event) => {
                        if (item)
                          setDrafts((current) => ({
                            ...current,
                            [item.key]: Array.from(event.target.value)
                              .slice(0, MAX_ALT_CODE_POINTS)
                              .join(""),
                          }));
                      }}
                    />
                  </label>
                  <Dialog.Root>
                    <Dialog.Trigger className={styles.help} disabled={locked}>
                      What is alt text?
                    </Dialog.Trigger>
                    <Dialog.Portal>
                      <Dialog.Backdrop className={styles.backdrop} />
                      <Dialog.Viewport className={styles.viewport}>
                        <Dialog.Popup className={styles.helpPopup}>
                          <Dialog.Title>Add descriptions</Dialog.Title>
                          <Dialog.Description>
                            Alt text describes what is in an image for people
                            who cannot see it. Keep it concise and include the
                            details needed to understand the picture. Adding a
                            description is optional.
                          </Dialog.Description>
                          <Dialog.Close className={styles.save}>
                            Got it
                          </Dialog.Close>
                        </Dialog.Popup>
                      </Dialog.Viewport>
                    </Dialog.Portal>
                  </Dialog.Root>
                </div>
              </Tabs.Panel>
            </Tabs.Root>
            {error ? (
              <p className={styles.cropError} role="alert">
                {error}
              </p>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
