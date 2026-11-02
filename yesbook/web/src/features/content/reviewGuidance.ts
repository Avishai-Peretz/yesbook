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
    "Review the flagged pages, then re-upload or pin a stronger extractor and submit again.";
  if (missingFigures && doclingUnset) {
    recommendedAction =
      "Start Docling locally (make dev / docker compose up docling) so Automatic+images can extract figures, or pin Gemini/OpenAI and re-submit.";
  } else if (missingFigures && chainExhausted) {
    recommendedAction =
      "No further extractors were configured after PDFium. Enable Docling (YESBOOK_DOCLING_URL) or set a Gemini/OpenAI key, then re-submit.";
  } else if (missingFigures) {
    recommendedAction =
      "Figures were missing from the accepted text pass. Pin Gemini (or enable Docling) and re-submit so the chain can escalate.";
  } else if (chainExhausted) {
    recommendedAction =
      "The extractor chain ended with no further tools configured. Enable Docling or a managed vision extractor, then re-submit.";
  } else if (doclingUnset) {
    recommendedAction =
      "Docling was unset during Automatic+images. Start Docling for local figure/layout extraction, then re-submit.";
  }

  return {
    coverageLabel: `${Math.round(content.coverage * 100)}% accepted`,
    failedCodes: unique([...failedCodes, ...decisionCodes.filter((code) => code === "figures_missing" || code === "extractor_unset" || code === "chain_exhausted")]),
    failedChecks,
    missingFigures,
    doclingUnset,
    chainExhausted,
    recommendedAction,
  };
}
