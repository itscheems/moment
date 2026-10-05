import type { Editor } from "@tiptap/core";
import { useEditorState } from "@tiptap/react";
import { useMemo } from "react";
import { OutlineRail } from "../../../../components/content/ArticleOutline";

export function EditorOutline({ editor }: { editor: Editor }) {
  const headings = useEditorState({
    editor,
    selector: ({ editor: current }) => current.storage.tableOfContents.content,
  });
  const items = useMemo(
    () =>
      headings.map((heading) => ({
        id: heading.id,
        title: heading.textContent,
        level: heading.originalLevel,
        element: heading.dom,
        activate: () => {
          editor
            .chain()
            .focus(undefined, { scrollIntoView: false })
            .setTextSelection(heading.pos + 1)
            .run();
        },
      })),
    [editor, headings],
  );
  return <OutlineRail items={items} />;
}
