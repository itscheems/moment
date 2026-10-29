import { Image } from "@content-notion/components/nodes/image-node/image-node-extension";
import { mediaIdFromUrl, mediaPreviewUrl } from "../../api/mediaUrls";

/** The template image stores a Moment media identity; its URL is derived at render time. */
export const ManagedImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      media_id: {
        default: null,
        parseHTML: (element) => {
          const img =
            element.tagName === "IMG" ? element : element.querySelector("img");
          return (
            img?.getAttribute("data-media-id") ??
            mediaIdFromUrl(img?.getAttribute("src"))
          );
        },
        renderHTML: (attrs) =>
          attrs.media_id ? { "data-media-id": attrs.media_id } : {},
      },
    };
  },
  parseHTML() {
    return (this.parent?.() ?? []).map((rule) => ({
      ...rule,
      getAttrs: (element: HTMLElement) => {
        const img =
          element.tagName === "IMG" ? element : element.querySelector("img");
        if (!img?.getAttribute("data-media-id")) return false;
        return typeof rule.getAttrs === "function"
          ? rule.getAttrs(element)
          : null;
      },
    }));
  },
  renderHTML(props) {
    const mediaId =
      props.node.attrs.media_id ?? mediaIdFromUrl(props.node.attrs.src);
    const node = mediaId
      ? props.node.type.create(
          { ...props.node.attrs, src: mediaPreviewUrl(mediaId) },
          props.node.content,
          props.node.marks,
        )
      : props.node;
    const output = this.parent!({ ...props, node });
    // Product-owned delivery attributes apply to the native image/figure markup.
    const visit = (value: unknown) => {
      if (!Array.isArray(value)) return;
      if (value[0] === "img")
        Object.assign(value[1], {
          loading: "lazy",
          decoding: "async",
          "data-media-id": mediaId,
        });
      else value.forEach(visit);
    };
    visit(output);
    return output;
  },
  addInputRules() {
    return [];
  },
});
