import { afterEach, describe, expect, it } from "vitest";
import { parseMapCard, mapCardPayload, type MapCardData } from "./types";
import {
  mapBounds,
  mapCoordinate,
  mapConnections,
  googleMapUrl,
} from "./geometry";
import {
  createComposeDraft,
  saveComposeDraft,
  loadComposeDraft,
  fingerprintComposeDraft,
} from "../compose/draft";
import {
  keepDraft,
  listKeptDrafts,
  dropKeptDraftByContent,
} from "../compose/drafts";

const card: MapCardData = {
  places: [
    {
      name: "银座",
      displayName: "日本东京银座",
      latitude: 35.6712345,
      longitude: 139.7654321,
      coordinateSystem: "wgs84",
    },
  ],
  connectPoints: false,
};
const scope = "0123456789abcdef0123456789abcdef." + "a".repeat(32);
afterEach(() => localStorage.clear());

describe("portable map card behavior", () => {
  it("retains valid antipodal place connections without rejecting the map", () => {
    const places = [
      { ...card.places[0], longitude: 0, latitude: 45 },
      { ...card.places[0], longitude: 180, latitude: -45 },
    ];
    expect(
      mapConnections({ places, connectPoints: true }).features,
    ).toHaveLength(1);
  });

  it("keeps source coordinates and exact map values through JSON and drafts", () => {
    expect(parseMapCard(mapCardPayload(card))).toEqual(card);
    const draft = createComposeDraft("", undefined, new Date(), card);
    expect(saveComposeDraft(scope, draft)).toBe(true);
    expect(loadComposeDraft(scope)?.mapCard).toEqual(card);
    expect(fingerprintComposeDraft("", card)).not.toBe(
      fingerprintComposeDraft(""),
    );
    expect(keepDraft(scope, "", undefined, card)?.mapCard).toEqual(card);
    expect(listKeptDrafts(scope)).toHaveLength(1);
    const other = { ...card, places: [{ ...card.places[0], name: "东京站" }] };
    keepDraft(scope, "", undefined, other);
    expect(listKeptDrafts(scope)).toHaveLength(2);
    dropKeptDraftByContent(scope, "", undefined, card);
    expect(listKeptDrafts(scope).map((draft) => draft.mapCard)).toEqual([
      other,
    ]);
  });

  it("normalizes only the display coordinates, without mutating stored points", () => {
    const point = {
      ...card.places[0],
      longitude: 116.4102445,
      latitude: 39.9164043,
      coordinateSystem: "gcj02" as const,
    };
    const [longitude, latitude] = mapCoordinate(point);
    expect(longitude).toBeCloseTo(116.404, 4);
    expect(latitude).toBeCloseTo(39.915, 4);
    expect(point.longitude).toBe(116.4102445);
    expect(googleMapUrl(point)).toContain("api=1");
  });

  it("frames the short date-line interval and does not create lines by default", () => {
    const places = [
      { ...card.places[0], longitude: 179, latitude: -17 },
      { ...card.places[0], longitude: -179, latitude: -14 },
    ];
    const [west, east] = mapBounds(places);
    expect(east[0] - west[0]).toBe(2);
    expect(
      mapConnections({ places, connectPoints: false }).features,
    ).toHaveLength(0);
    expect(
      mapConnections({ places, connectPoints: true }).features[0].geometry.type,
    ).toBe("MultiLineString");
  });

  it("rejects points outside the bounded card contract", () => {
    const wire = mapCardPayload(card);
    expect(() => parseMapCard({ ...wire, places: [] })).toThrow();
    expect(() => parseMapCard({ ...wire, connect_points: true })).toThrow();
    expect(() =>
      parseMapCard({ ...wire, places: Array(21).fill(wire.places[0]) }),
    ).toThrow();
    expect(() =>
      parseMapCard({ ...wire, places: [{ ...wire.places[0], latitude: 91 }] }),
    ).toThrow();
  });
});
