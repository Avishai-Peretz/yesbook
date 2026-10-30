import { describe, expect, it } from "vitest";
import {
  BLOCK_KINDS,
  contentResponseSchema,
  extractedContentSchema,
} from "../api/contracts";
import {
  emptyContent,
  malformedContentBody,
  partialContent,
  readyContent,
  reviewContent,
  rtlContent,
} from "./content";

describe("content fixtures", () => {
  it("covers every current block kind", () => {
    const kinds = new Set(
      [readyContent, partialContent, reviewContent, rtlContent].flatMap((response) =>
        response.content.blocks.map((block) => block.kind),
      ),
    );
    expect(kinds).toEqual(new Set(BLOCK_KINDS));
  });

  it("includes RTL text with document direction", () => {
    const rtlBlocks = rtlContent.content.blocks.filter((block) => block.direction === "rtl");
    expect(rtlBlocks.length).toBeGreaterThan(0);
    expect(rtlBlocks.some((block) => block.kind === "paragraph")).toBe(true);
    expect(rtlBlocks.some((block) => block.kind === "table")).toBe(true);
  });

  it("models ready, partial, and needs_review outcomes with coverage and warnings", () => {
    expect(readyContent.content).toMatchObject({ status: "ready", coverage: 1 });
    expect(partialContent.content).toMatchObject({ status: "partial", coverage: 0.5 });
    expect(partialContent.content.missing?.length).toBeGreaterThan(0);
    expect(reviewContent.content).toMatchObject({ status: "needs_review", coverage: 0.75 });
    expect(reviewContent.content.review?.length).toBeGreaterThan(0);
    expect(emptyContent.content.blocks).toHaveLength(0);
    const figure = readyContent.content.blocks.find((block) => block.kind === "figure");
    expect(figure?.asset?.url).toMatch(/leaf-cross-section/);
    expect(reviewContent.content.blocks.find((block) => block.kind === "figure")?.asset_url).toMatch(
      /plant-cell/,
    );
  });

  it("rejects malformed blocks that omit required fields", () => {
    expect(contentResponseSchema.safeParse(malformedContentBody).success).toBe(false);
    expect(
      extractedContentSchema.safeParse({
        ...readyContent.content,
        blocks: [{ kind: "table", page: 1, provenance: readyContent.content.blocks[0]?.provenance }],
      }).success,
    ).toBe(false);
  });
});
