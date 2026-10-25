-- SPDX-License-Identifier: Apache-2.0
-- Keeps the OTLP status message of error spans.

-- +goose Up
ALTER TABLE spans ADD COLUMN status_message TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Downgrades are not supported (operations.md); restore a .pre-v<N> backup instead.
SELECT 1;
