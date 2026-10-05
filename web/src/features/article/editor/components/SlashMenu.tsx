import type { Editor } from "@tiptap/core";
import { useEditorState } from "@tiptap/react";
import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import styles from "../Editor.module.css";

export function slashQuery(editor: Editor) {
  const { empty, $from } = editor.state.selection;
  if (!editor.isEditable || !empty || $from.parent.type.name !== "paragraph")
    return null;
  const text = $from.parent.textBetween(0, $from.parentOffset);
  if (!/^\/[^\s/]*$/.test(text)) return null;
  return {
    from: $from.start(),
    to: $from.pos,
    query: text.slice(1).toLowerCase(),
  };
}

export function SlashMenu({
  editor,
  onPickImage,
}: {
  editor: Editor;
  onPickImage: () => void;
}) {
  const query = useEditorState({
    editor,
    selector: ({ editor: current }) => slashQuery(current),
  });
  const signature = query ? `${query.from}:${query.to}:${query.query}` : "";
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [selected, setSelected] = useState(0);
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const menuRef = useRef<HTMLDivElement>(null);
  const id = useId();
  const commands = useMemo(
    () => [
      {
        label: "正文",
        search: "paragraph p",
        run: () => editor.commands.setParagraph(),
      },
      ...([1, 2, 3] as const).map((level) => ({
        label: `标题 ${level}`,
        search: `heading h${level}`,
        run: () => editor.commands.setHeading({ level }),
      })),
      {
        label: "引用",
        search: "quote",
        run: () => editor.commands.toggleBlockquote(),
      },
      {
        label: "无序列表",
        search: "bullet list ul",
        run: () => editor.commands.toggleBulletList(),
      },
      {
        label: "有序列表",
        search: "ordered list ol",
        run: () => editor.commands.toggleOrderedList(),
      },
      {
        label: "代码块",
        search: "code",
        run: () => editor.commands.setCodeBlock(),
      },
      {
        label: "分隔线",
        search: "divider hr",
        run: () => editor.commands.setHorizontalRule(),
      },
      {
        label: "表格",
        search: "table",
        run: () =>
          editor.commands.insertTable({
            rows: 3,
            cols: 3,
            withHeaderRow: true,
          }),
      },
      { label: "图片", search: "image photo", run: onPickImage },
    ],
    [editor, onPickImage],
  );
  const filtered = commands.filter((command) =>
    `${command.label} ${command.search}`.includes(query?.query ?? ""),
  );
  const active =
    signature !== "" && signature !== dismissed && filtered.length > 0;
  const index = Math.min(selected, filtered.length - 1);

  useEffect(() => {
    setSelected(0);
    if (!signature) setDismissed(null);
  }, [signature]);
  useLayoutEffect(() => {
    if (!active || !query) return;
    const place = (event?: Event) => {
      if (
        event?.target instanceof Node &&
        menuRef.current?.contains(event.target)
      )
        return;
      const caret = editor.view.coordsAtPos(query.to);
      const height = menuRef.current?.offsetHeight ?? 280;
      setPosition({
        left: Math.max(8, Math.min(caret.left, window.innerWidth - 240)),
        top:
          caret.bottom + height + 12 <= window.innerHeight
            ? caret.bottom + 8
            : Math.max(8, caret.top - height - 8),
      });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [active, editor, query]);

  useEffect(() => {
    if (!active || !query) return;
    const dom = editor.view.dom;
    const choose = (at: number) => {
      editor
        .chain()
        .focus()
        .deleteRange({ from: query.from, to: query.to })
        .run();
      filtered[at]?.run();
    };
    const keydown = (event: KeyboardEvent) => {
      if (event.isComposing) return;
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        event.stopPropagation();
        setSelected(
          (previous) =>
            (previous +
              (event.key === "ArrowDown" ? 1 : -1) +
              filtered.length) %
            filtered.length,
        );
      } else if (event.key === "Enter") {
        event.preventDefault();
        event.stopPropagation();
        choose(index);
      } else if (event.key === "Escape" || event.key === "Tab") {
        setDismissed(signature);
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
        }
      }
    };
    dom.setAttribute("aria-controls", id);
    dom.setAttribute("aria-activedescendant", `${id}-${index}`);
    dom.addEventListener("keydown", keydown, true);
    return () => {
      dom.removeEventListener("keydown", keydown, true);
      dom.removeAttribute("aria-controls");
      dom.removeAttribute("aria-activedescendant");
    };
  }, [active, editor, filtered, id, index, query, signature]);
  useEffect(() => {
    if (!active) return;
    // Reveal keyboard selection changes, without undoing pointer scrolling.
    menuRef.current
      ?.querySelector(`[data-selected="true"]`)
      ?.scrollIntoView?.({ block: "nearest" });
  }, [active, index, signature]);
  useEffect(() => {
    const dismiss = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node))
        setDismissed(signature);
    };
    if (active) document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [active, signature]);
  if (!active || !query) return null;
  return createPortal(
    <div
      aria-label="插入内容"
      className={styles.slashMenu}
      id={id}
      ref={menuRef}
      role="listbox"
      style={position}
    >
      {filtered.map((command, at) => (
        <button
          aria-selected={index === at}
          className={styles.slashOption}
          data-selected={index === at}
          id={`${id}-${at}`}
          key={command.label}
          role="option"
          tabIndex={-1}
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            editor
              .chain()
              .focus()
              .deleteRange({ from: query.from, to: query.to })
              .run();
            command.run();
          }}
        >
          {command.label}
        </button>
      ))}
    </div>,
    document.body,
  );
}
