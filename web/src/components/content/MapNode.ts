import { t } from "../../i18n";
import { Node } from "@tiptap/core";
import { mapCardPayload, parseMapCard } from "../../features/maps/types";

// The stored node is a value snapshot, not an iframe or a live provider query.
export const MapNode = Node.create({
  name: "mapCard",
  group: "block",
  atom: true,
  draggable: true,
  addAttributes() {
    return {
      id: { default: null },
      map_card: { default: null, rendered: false },
    };
  },
  parseHTML() {
    return [
      {
        tag: "figure[data-map-card]",
        getAttrs: (element) => {
          const raw = element.getAttribute("data-map-card");
          if (!raw) return false;
          try {
            return { map_card: mapCardPayload(parseMapCard(JSON.parse(raw))) };
          } catch {
            return false;
          }
        },
      },
    ];
  },
  renderHTML({ node }) {
    if (!node.attrs.map_card)
      return ["figure", { "data-map-card": "" }, t("地图")];
    const card = parseMapCard(node.attrs.map_card);
    return [
      "figure",
      {
        "data-map-card": JSON.stringify(mapCardPayload(card)),
        ...(node.attrs.id ? { id: node.attrs.id } : {}),
      },
      ["figcaption", {}, t("地图")],
      [
        "ol",
        {},
        ...card.places.map((place) => [
          "li",
          {},
          `${place.name} · ${place.displayName}`,
        ]),
      ],
    ];
  },
});
