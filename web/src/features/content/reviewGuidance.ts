import type {
  ExtractedContent,
  PathDecision,
  ReviewItem,
} from "../../api/contracts";
import type { PathDecisionView } from "../documents/model";

export interface ReviewGuidance {
  coverageLabel: string;
  failedCodes: string[];
  failedChecks: string[];
  missingFigures: boolean;
  doclingUnset: boolean;
  chainExhausted: boolean;
  recommendedAction: string;
}

function unique(values: readonly string[]): string[] {
  return [...new Set(values.filter(Boolean))];
}

export function collectFailedCodes(items: readonly ReviewItem[]): string[] {
  return unique(items.map((item) => item.code ?? "").filter(Boolean));
}

export function collectFailedChecks(items: readonly ReviewItem[]): string[] {
  return unique(items.flatMap((item) => item.failed_checks ?? []));
}

export function buildReviewGuidance(
  content: ExtractedContent,
  decisions: readonly PathDecisionView[] | readonly PathDecision[] = [],
): ReviewGuidance {
  const review = content.review ?? [];
  const failedCodes = collectFailedCodes(review);
  const failedChecks = collectFailedChecks(review);
  const decisionCodes = decisions.map((item) =>
    "reasonCode" in item ? item.reasonCode : item.reason_code,
  );
  const doclingUnset = decisionCodes.includes("extractor_unset");
  const chainExhausted = decisionCodes.includes("chain_exhausted");
  const missingFigures =
    failedCodes.includes("figures_missing") ||
    failedChecks.includes("figures") ||
    decisionCodes.includes("figures_missing");

  let recommendedAction =
    "Review the flagged pages, then upload the document again with a stronger extraction mode.";
  if (missingFigures && doclingUnset) {
    recommendedAction =
      "Figures were missing and Docling was not available during processing. Upload again after Docling is configured, or pin Gemini/OpenAI.";
  } else if (missingFigures && chainExhausted) {
    recommendedAction =
      "No further extractors were configured after PDFium. Upload again after enabling Docling or a managed vision extractor, or pin Gemini/OpenAI.";
  } else if (missingFigures) {
    recommendedAction =
      "Figures were missing from the accepted text pass. Upload again and pin Gemini (or enable Docling) so the chain can escalate.";
  } else if (chainExhausted) {
    recommendedAction =
      "The extractor chain ended with no further tools configured. Upload again after enabling Docling or a managed vision extractor.";
  } else if (doclingUnset) {
    recommendedAction =
      "Docling was unset during Automatic+images. Upload again after Docling is available for figure and layout extraction.";
  }

  return {
    coverageLabel: `${String(Math.round(content.coverage * 100))}% accepted`,
    failedCodes: unique([...failedCodes, ...decisionCodes.filter((code) => code === "figures_missing" || code === "extractor_unset" || code === "chain_exhausted")]),
    failedChecks,
    missingFigures,
    doclingUnset,
    chainExhausted,
    recommendedAction,
  };
}
