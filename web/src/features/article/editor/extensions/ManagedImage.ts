import { Node, mergeAttributes } from "@tiptap/core";

import { mediaPreviewUrl } from "../../../../api/mediaUrls";

/**
 * A picture in an article body.
 *
 * Tiptap's own Image node keeps a `src`, and this one cannot: the server
 * refuses a body that names an address it does not host, so the document
 * stores the media id and the address is derived from it when the picture is
 * drawn. Storing the URL as well would be a second copy of a fact the recipe
 * version can change under us -- the same reason nothing else in this client
 * writes a media URL down (api/mediaUrls.ts).
 *
 * The attributes are spelled the way the wire spells them, `media_id` and not
 * `mediaId`, because Tiptap serialises attribute names straight into the
 * document JSON. A camel-cased name here would need renaming on the way out,
 * and a rename is where two spellings start to drift.
 */
export const ManagedImage = Node.create({
  name: "image",
  group: "block",
  atom: true,
  draggable: true,

  addAttributes() {
    return {
      media_id: {
        default: null,
        parseHTML: (element) => element.getAttribute("data-media-id"),
        renderHTML: (attributes) => {
          const mediaId: unknown = attributes.media_id;
          if (typeof mediaId !== "string" || mediaId === "") return {};
          // `src` is for the editor's own eyes. It never reaches the document,
          // which carries only the id the server will render from.
          return {
            "data-media-id": mediaId,
            src: mediaPreviewUrl(mediaId),
          };
        },
      },
      alt: {
        default: null,
        parseHTML: (element) => element.getAttribute("alt"),
        renderHTML: (attributes) => {
          const alt: unknown = attributes.alt;
          return typeof alt === "string" && alt !== "" ? { alt } : {};
        },
      },
    };
  },

  parseHTML() {
    // Only a managed picture is recognised. Pasting an `<img>` from anywhere
    // else drops it rather than writing an address the server will refuse.
    return [{ tag: "img[data-media-id]" }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["img", mergeAttributes(HTMLAttributes, { draggable: "false" })];
  },
});
