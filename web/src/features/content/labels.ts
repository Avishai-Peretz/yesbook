import type {
  BlockKind,
  PublishedContentStatus,
  ReviewItem,
} from "../../api/contracts";

const BLOCK_KIND_LABELS: Record<BlockKind, string> = {
  paragraph: "Paragraph",
  heading: "Heading",
  table: "Table",
  formula: "Formula",
  figure: "Figure",
};

const OUTCOME_LABELS: Record<PublishedContentStatus, string> = {
  ready: "Ready",
  partial: "Partial",
  needs_review: "Needs review",
};

export function blockKindLabel(kind: BlockKind): string {
  return BLOCK_KIND_LABELS[kind];
}

export function contentOutcomeLabel(status: PublishedContentStatus): string {
  return OUTCOME_LABELS[status];
}

export function reviewItemLabel(item: ReviewItem): string {
  const pages =
    item.pages.length === 0
      ? "unspecified pages"
      : item.pages.length === 1
        ? `page ${String(item.pages[0])}`
        : `pages ${item.pages.join(", ")}`;
  return `${pages}: ${item.reason}`;
}

export function pageRangeLabel(page: number, endPage?: number): string {
  if (endPage !== undefined && endPage !== page) {
    return `Pages ${String(page)}–${String(endPage)}`;
  }
  return `Page ${String(page)}`;
}
