import {
  NodeViewWrapper,
  ReactNodeViewRenderer,
  type NodeViewProps,
} from "@tiptap/react";
import { MapNode } from "../../../components/content/MapNode";
import { MapCard } from "../../maps/MapCard";
import { MapCardEditor } from "../../maps/MapCardEditor";
import { parseMapCard, mapCardPayload } from "../../maps/types";

function MapNodeView({
  node,
  editor,
  updateAttributes,
  deleteNode,
  selected,
}: NodeViewProps) {
  const card = parseMapCard(node.attrs.map_card);
  return (
    <NodeViewWrapper
      contentEditable={false}
      data-map-node=""
      data-selected={selected ? "true" : undefined}
    >
      {editor.isEditable ? (
        <MapCardEditor
          value={card}
          onChange={(next) => {
            if (next) updateAttributes({ map_card: mapCardPayload(next) });
            else deleteNode();
          }}
        />
      ) : (
        <MapCard card={card} />
      )}
    </NodeViewWrapper>
  );
}

export const InteractiveMapNode = MapNode.extend({
  addNodeView() {
    return ReactNodeViewRenderer(MapNodeView);
  },
});
