import {
  ApiClient,
  hasExactKeys,
  invalidResponse,
  objectData,
  type LocationInput,
  parseLocationInput,
  requestInit,
} from "./client";
import { decodeResponse } from "./public";

export interface LocationBias {
  latitude: number;
  longitude: number;
}

export class LocationsApi extends ApiClient {
  async searchLocations(
    query: string,
    bias?: LocationBias,
    signal?: AbortSignal,
    scope: "auto" | "global" = "auto",
  ): Promise<LocationInput[]> {
    const params = new URLSearchParams({ q: query });
    if (scope === "global") params.set("scope", scope);
    if (bias) {
      params.set("latitude", bias.latitude.toString());
      params.set("longitude", bias.longitude.toString());
    }
    return this.locations(`/api/v1/locations/search?${params}`, signal);
  }

  async nearbyLocations(
    point: LocationBias,
    signal?: AbortSignal,
  ): Promise<LocationInput[]> {
    const params = new URLSearchParams({
      latitude: point.latitude.toString(),
      longitude: point.longitude.toString(),
    });
    return this.locations(`/api/v1/locations/nearby?${params}`, signal);
  }

  private async locations(
    endpoint: string,
    signal?: AbortSignal,
  ): Promise<LocationInput[]> {
    const response = await this.fetcher(endpoint, {
      ...requestInit("GET"),
      signal,
    });
    const { data, meta } = await decodeResponse(response);
    // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
    if (!hasExactKeys(meta, ["request_id"])) {
      throw invalidResponse("location metadata is invalid");
    }
    const record = objectData(data);
    // mirrors locationdto.Results (internal/transport/locationdto/dto.go)
    if (!hasExactKeys(record, ["items"]) || !Array.isArray(record.items)) {
      throw invalidResponse("location results are invalid");
    }
    return record.items.map(parseLocationInput);
  }
}

export const locationsApi = new LocationsApi();
