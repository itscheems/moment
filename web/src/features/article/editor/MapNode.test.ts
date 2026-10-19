import { describe, expect, it } from "vitest";
import {
  DOMParser as ProseMirrorParser,
  DOMSerializer,
} from "@tiptap/pm/model";
import { contentSchemas } from "../../../components/content/schema";
import { parseArticleDocument } from "../../../api/public";
import { toArticleDocument, toEditorContent } from "./document";
import { mapCardPayload, type MapCardData } from "../../maps/types";
const card: MapCardData = {
  places: [
    {
      name: "银座",
      displayName: "日本东京银座",
      latitude: 35.67,
      longitude: 139.76,
      coordinateSystem: "wgs84",
    },
  ],
  connectPoints: false,
};
describe("article map snapshots", () => {
  it("keeps map snapshots when copying article HTML", () => {
    const schema = contentSchemas.article;
    const doc = schema.nodeFromJSON({
      type: "doc",
      content: [
        {
          type: "mapCard",
          attrs: { id: "copy-map", map_card: mapCardPayload(card) },
        },
      ],
    });
    const element = document.createElement("div");
    element.append(
      DOMSerializer.fromSchema(schema).serializeFragment(doc.content),
    );
    const restored = ProseMirrorParser.fromSchema(schema).parse(element);
    expect(restored.firstChild?.type.name).toBe("mapCard");
    expect(restored.firstChild?.attrs.map_card).toEqual(mapCardPayload(card));
  });

  it("preserves the map node when an article is opened and saved", () => {
    const document = parseArticleDocument({
      schema_version: 3,
      type: "doc",
      content: [
        {
          type: "mapCard",
          attrs: { id: "tokyo-map", map_card: mapCardPayload(card) },
        },
      ],
    });
    expect(toArticleDocument(toEditorContent(document))).toEqual(document);
  });
});
