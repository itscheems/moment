import { lazy, Suspense, type ComponentProps } from "react";
import { RichContent } from "../../../components/content/RichContent";
const ReadingContent = lazy(() =>
  import("./ArticleReadingContent").then((module) => ({
    default: module.ArticleReadingContent,
  })),
);
/** Article-only interaction is loaded separately from the timeline renderer. */
export function ArticleReadingContent(
  props: ComponentProps<typeof ReadingContent>,
) {
  return (
    <Suspense
      fallback={<RichContent document={props.document} media={props.media} />}
    >
      <ReadingContent {...props} />
    </Suspense>
  );
}
