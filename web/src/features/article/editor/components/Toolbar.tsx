import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import type { Editor } from "@tiptap/core";
import { useEditorState } from "@tiptap/react";
import { useId, useState } from "react";

import { ARTICLE_CODE_LANGUAGES } from "../schema";
import styles from "../Editor.module.css";
import { EditorIcon } from "./EditorIcons";
import { LinkPopover } from "./LinkPopover";

const BLOCK_LABELS: ReadonlyArray<{ level: 0 | 1 | 2 | 3; label: string }> = [
  { level: 0, label: "正文" },
  { level: 1, label: "标题 1" },
  { level: 2, label: "标题 2" },
  { level: 3, label: "标题 3" },
];

function ToolButton({
  active = false,
  disabled = false,
  label,
  onClick,
  children,
}: {
  active?: boolean;
  disabled?: boolean;
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      aria-label={label}
      aria-pressed={active}
      className={styles.toolButton}
      disabled={disabled}
      onClick={onClick}
      title={label}
      type="button"
    >
      {children}
    </button>
  );
}

/**
 * The image's description, edited where the image is.
 *
 * `alt` is the only thing a reader who cannot see the picture will ever get
 * from it, and it is the one attribute of a picture the owner can write. It is
 * optional on the wire, so an empty field removes it rather than storing "".
 */
function ImageAltPopover({ editor }: { editor: Editor }) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const fieldId = useId();

  return (
    <Popover.Root
      onOpenChange={(next) => {
        if (next) {
          const current: unknown = editor.getAttributes("image").alt;
          setValue(typeof current === "string" ? current : "");
        }
        setOpen(next);
      }}
      open={open}
    >
      <Popover.Trigger className={styles.toolButton}>图片说明</Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner align="start" side="bottom" sideOffset={6}>
          <Popover.Popup aria-label="图片说明" className={styles.popup}>
            <div className={styles.field}>
              <label className={styles.fieldLabel} htmlFor={fieldId}>
                图片说明
              </label>
              <input
                autoFocus
                className={styles.input}
                id={fieldId}
                onChange={(event) => setValue(event.target.value)}
                placeholder="看不到这张图的人会读到这句话"
                type="text"
                value={value}
              />
            </div>
            <div className={styles.popupActions}>
              <button
                className={styles.primaryAction}
                onClick={() => {
                  const alt = value.trim();
                  editor
                    .chain()
                    .focus()
                    .updateAttributes("image", { alt: alt === "" ? null : alt })
                    .run();
                  setOpen(false);
                }}
                type="button"
              >
                应用
              </button>
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/**
 * The one row of controls above the sheet.
 *
 * Every button here produces something the allowlist names. There is no button
 * for anything else, which is the first of the two defences against writing a
 * body the server refuses -- the second being the serialiser, which is what
 * catches whatever arrives by paste.
 */
export function Toolbar({
  editor,
  onPickImage,
  uploading,
}: {
  editor: Editor;
  onPickImage: () => void;
  uploading: boolean;
}) {
  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current.isActive("bold"),
      italic: current.isActive("italic"),
      strike: current.isActive("strike"),
      underline: current.isActive("underline"),
      code: current.isActive("code"),
      link: current.isActive("link"),
      blockquote: current.isActive("blockquote"),
      bulletList: current.isActive("bulletList"),
      orderedList: current.isActive("orderedList"),
      codeBlock: current.isActive("codeBlock"),
      inTable: current.isActive("table"),
      onImage: current.isActive("image"),
      heading:
        ([1, 2, 3] as const).find((level) =>
          current.isActive("heading", { level }),
        ) ?? 0,
    }),
  });

  const blockLabel =
    BLOCK_LABELS.find((entry) => entry.level === state.heading)?.label ??
    "正文";

  return (
    <div aria-label="排版" className={styles.toolBar} role="toolbar">
      <div className={styles.toolGroup}>
        <ToolButton
          active={state.bold}
          label="加粗"
          onClick={() => editor.chain().focus().toggleBold().run()}
        >
          <EditorIcon name="bold" />
        </ToolButton>
        <ToolButton
          active={state.italic}
          label="斜体"
          onClick={() => editor.chain().focus().toggleItalic().run()}
        >
          <EditorIcon name="italic" />
        </ToolButton>
        <ToolButton
          active={state.strike}
          label="删除线"
          onClick={() => editor.chain().focus().toggleStrike().run()}
        >
          <EditorIcon name="strike" />
        </ToolButton>
        <ToolButton
          active={state.underline}
          label="下划线"
          onClick={() => editor.chain().focus().toggleUnderline().run()}
        >
          <EditorIcon name="underline" />
        </ToolButton>
        <ToolButton
          active={state.code}
          label="行内代码"
          onClick={() => editor.chain().focus().toggleCode().run()}
        >
          <EditorIcon name="code" />
        </ToolButton>
      </div>

      <span aria-hidden="true" className={styles.toolDivider} />

      <Menu.Root>
        <Menu.Trigger aria-label="段落样式" className={styles.toolButton}>
          <span>{blockLabel}</span>
          <EditorIcon name="chevron" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner align="start" side="bottom" sideOffset={6}>
            <Menu.Popup aria-label="段落样式" className={styles.menuPopup}>
              {BLOCK_LABELS.map(({ level, label }) => (
                <Menu.Item
                  className={styles.menuItem}
                  data-selected={state.heading === level || undefined}
                  key={label}
                  onClick={() => {
                    if (level === 0) {
                      editor.chain().focus().setParagraph().run();
                      return;
                    }
                    editor.chain().focus().setHeading({ level }).run();
                  }}
                >
                  {label}
                </Menu.Item>
              ))}
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <span aria-hidden="true" className={styles.toolDivider} />

      <div className={styles.toolGroup}>
        <ToolButton
          active={state.blockquote}
          label="引用"
          onClick={() => editor.chain().focus().toggleBlockquote().run()}
        >
          <EditorIcon name="quote" />
        </ToolButton>
        <ToolButton
          active={state.bulletList}
          label="无序列表"
          onClick={() => editor.chain().focus().toggleBulletList().run()}
        >
          <EditorIcon name="bullet-list" />
        </ToolButton>
        <ToolButton
          active={state.orderedList}
          label="有序列表"
          onClick={() => editor.chain().focus().toggleOrderedList().run()}
        >
          <EditorIcon name="ordered-list" />
        </ToolButton>
        <LinkPopover
          editor={editor}
          trigger={
            <button
              aria-label="链接"
              aria-pressed={state.link}
              className={styles.toolButton}
              title="链接"
              type="button"
            >
              <EditorIcon name="link" />
            </button>
          }
        />
        <ToolButton
          active={state.inTable}
          label="表格"
          onClick={() =>
            editor
              .chain()
              .focus()
              .insertTable({ rows: 3, cols: 3, withHeaderRow: true })
              .run()
          }
        >
          <EditorIcon name="table" />
        </ToolButton>
      </div>

      <span aria-hidden="true" className={styles.toolDivider} />

      <Menu.Root>
        <Menu.Trigger aria-label="插入" className={styles.toolButton}>
          <span>插入</span>
          <EditorIcon name="chevron" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner align="start" side="bottom" sideOffset={6}>
            <Menu.Popup aria-label="插入" className={styles.menuPopup}>
              <Menu.Item
                className={styles.menuItem}
                disabled={uploading}
                onClick={onPickImage}
              >
                <EditorIcon name="image" />
                <span>图片</span>
              </Menu.Item>
              <Menu.Item
                className={styles.menuItem}
                onClick={() => editor.chain().focus().toggleCodeBlock().run()}
              >
                <EditorIcon name="code" />
                <span>代码块</span>
              </Menu.Item>
              <Menu.Item
                className={styles.menuItem}
                onClick={() => editor.chain().focus().setHorizontalRule().run()}
              >
                <EditorIcon name="rule" />
                <span>分隔线</span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      {state.codeBlock ? (
        <>
          <span aria-hidden="true" className={styles.toolDivider} />
          <label className={styles.toolGroup}>
            <span className={styles.srOnly}>代码语言</span>
            <select
              className={styles.toolButton}
              onChange={(event) =>
                editor
                  .chain()
                  .focus()
                  .updateAttributes("codeBlock", {
                    language:
                      event.target.value === "" ? null : event.target.value,
                  })
                  .run()
              }
              value={languageOf(editor)}
            >
              <option value="">无语言</option>
              {ARTICLE_CODE_LANGUAGES.map((language) => (
                <option key={language} value={language}>
                  {language}
                </option>
              ))}
            </select>
          </label>
        </>
      ) : null}

      {state.inTable ? (
        <>
          <span aria-hidden="true" className={styles.toolDivider} />
          <Menu.Root>
            <Menu.Trigger aria-label="表格操作" className={styles.toolButton}>
              <span>表格</span>
              <EditorIcon name="chevron" />
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Positioner align="start" side="bottom" sideOffset={6}>
                <Menu.Popup aria-label="表格操作" className={styles.menuPopup}>
                  <Menu.Item
                    className={styles.menuItem}
                    onClick={() => editor.chain().focus().addRowAfter().run()}
                  >
                    在下方加一行
                  </Menu.Item>
                  <Menu.Item
                    className={styles.menuItem}
                    onClick={() =>
                      editor.chain().focus().addColumnAfter().run()
                    }
                  >
                    在右侧加一列
                  </Menu.Item>
                  <Menu.Item
                    className={styles.menuItem}
                    onClick={() => editor.chain().focus().deleteRow().run()}
                  >
                    删除这一行
                  </Menu.Item>
                  <Menu.Item
                    className={styles.menuItem}
                    onClick={() => editor.chain().focus().deleteColumn().run()}
                  >
                    删除这一列
                  </Menu.Item>
                  <Menu.Item
                    className={`${styles.menuItem} ${styles.menuItemDanger}`}
                    onClick={() => editor.chain().focus().deleteTable().run()}
                  >
                    删除整个表格
                  </Menu.Item>
                </Menu.Popup>
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>
        </>
      ) : null}

      {state.onImage ? (
        <>
          <span aria-hidden="true" className={styles.toolDivider} />
          <div className={styles.toolGroup}>
            <ImageAltPopover editor={editor} />
            <ToolButton
              label="删除图片"
              onClick={() => editor.chain().focus().deleteSelection().run()}
            >
              <EditorIcon name="unlink" />
            </ToolButton>
          </div>
        </>
      ) : null}
    </div>
  );
}

function languageOf(editor: Editor): string {
  const language: unknown = editor.getAttributes("codeBlock").language;
  return typeof language === "string" ? language : "";
}
