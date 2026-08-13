CREATE TABLE entry_location (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> '' AND name = trim(name) AND length(name) <= 160),
    display_name TEXT NOT NULL CHECK (display_name <> '' AND display_name = trim(display_name) AND length(display_name) <= 500),
    country_code TEXT NOT NULL CHECK (
        country_code = '' OR (
            length(country_code) = 2
            AND country_code = upper(country_code)
            AND country_code NOT GLOB '*[^A-Z]*'
        )
    ),
    country TEXT NOT NULL CHECK (country = trim(country) AND length(country) <= 120),
    region TEXT NOT NULL CHECK (region = trim(region) AND length(region) <= 120),
    city TEXT NOT NULL CHECK (city = trim(city) AND length(city) <= 120),
    district TEXT NOT NULL CHECK (district = trim(district) AND length(district) <= 120),
    category TEXT NOT NULL CHECK (category = trim(category) AND length(category) <= 120),
    provider TEXT NOT NULL CHECK (provider IN ('manual', 'amap', 'geoapify')),
    provider_place_id TEXT NOT NULL CHECK (provider_place_id = trim(provider_place_id) AND length(provider_place_id) <= 512),
    latitude_e7 INTEGER,
    longitude_e7 INTEGER,
    coordinate_system TEXT CHECK (coordinate_system IN ('wgs84', 'gcj02')),
    CHECK (
        (latitude_e7 IS NULL AND longitude_e7 IS NULL AND coordinate_system IS NULL)
        OR (
            latitude_e7 BETWEEN -900000000 AND 900000000
            AND longitude_e7 BETWEEN -1800000000 AND 1800000000
            AND coordinate_system IS NOT NULL
        )
    ),
    CHECK (provider <> 'manual' OR provider_place_id = '')
) STRICT;
