import {
  figureAssetURL,
  type ContentBlock as ContentBlockModel,
} from "../../api/contracts";
import { extractorPathFor, extractorPathLabel } from "../documents/model";
import { blockKindLabel, pageRangeLabel } from "./labels";

interface ContentBlockProps {
  block: ContentBlockModel;
  index: number;
}

function ProvenanceDetails({ block }: { block: ContentBlockModel }) {
  const { provenance } = block;
  const pathLabel = extractorPathLabel(extractorPathFor(provenance.extractor));
  return (
    <details className="block-provenance">
      <summary>Provenance</summary>
      <dl>
        <div>
          <dt>Path</dt>
          <dd data-path={extractorPathFor(provenance.extractor)}>{pathLabel}</dd>
        </div>
        <div>
          <dt>Extractor</dt>
          <dd>
            {provenance.extractor} {provenance.extractor_version}
          </dd>
        </div>
        <div>
          <dt>Target</dt>
          <dd>{provenance.target_id}</dd>
        </div>
        <div>
          <dt>Attempt</dt>
          <dd>{provenance.attempt_id}</dd>
        </div>
      </dl>
    </details>
  );
}

function TableBlock({ block, caption }: { block: ContentBlockModel; caption: string }) {
  const rows = block.rows ?? [];
  const [header = [], ...body] = rows;
  const direction = block.direction ?? "ltr";

  return (
    <div className="table-wrap">
      <table dir={direction}>
        <caption>{caption}</caption>
        {header.length > 0 ? (
          <thead>
            <tr>
              {header.map((cell, cellIndex) => (
                <th key={`h-${String(cellIndex)}`} scope="col">
                  {cell || `Column ${String(cellIndex + 1)}`}
                </th>
              ))}
            </tr>
          </thead>
        ) : null}
        <tbody>
          {body.length === 0 && header.length === 0 ? (
            <tr>
              <td>No table cells were extracted.</td>
            </tr>
          ) : (
            body.map((row, rowIndex) => (
              <tr key={`r-${String(rowIndex)}`}>
                {row.map((cell, cellIndex) => (
                  <td key={`c-${String(rowIndex)}-${String(cellIndex)}`}>{cell}</td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

function FigureBlock({
  block,
  direction,
}: {
  block: ContentBlockModel;
  direction: "ltr" | "rtl";
}) {
  const assetURL = figureAssetURL(block);
  const caption = block.text?.trim() ? block.text.trim() : null;
  const contentType = block.asset?.content_type;

  return (
    <figure className="content-figure" dir={direction}>
      {assetURL ? (
        <img
          className="content-figure-image"
          src={assetURL}
          alt={caption ?? "Extracted figure"}
          loading="lazy"
          {...(contentType ? { "data-content-type": contentType } : {})}
        />
      ) : null}
      {caption ? <figcaption>{caption}</figcaption> : null}
      {!assetURL ? (
        <p className="block-meta">
          {caption ? "Caption only — no image asset URL was published." : "Extracted figure"}
        </p>
      ) : null}
    </figure>
  );
}

export function ContentBlockView({ block, index }: ContentBlockProps) {
  const kindLabel = blockKindLabel(block.kind);
  const pageLabel = pageRangeLabel(block.page, block.end_page);
  const headingId = `content-block-${String(index)}`;
  const direction = block.direction ?? "ltr";
  const caption = `${kindLabel} on ${pageLabel.toLowerCase()}`;

  return (
    <article className="content-block" aria-labelledby={headingId} data-kind={block.kind}>
      <header className="content-block-header">
        <h3 id={headingId}>
          {kindLabel}
          <span className="visually-hidden">, {pageLabel}</span>
        </h3>
        <p className="block-meta" aria-hidden="true">
          {pageLabel}
          {block.direction === "rtl" ? " · RTL" : null}
        </p>
      </header>

      {block.kind === "heading" ? (
        <p
          className="content-heading-text content-text"
          dir={direction}
          lang={direction === "rtl" ? "he" : undefined}
        >
          {block.text}
        </p>
      ) : null}

      {block.kind === "paragraph" ? (
        <p
          className="content-text"
          dir={direction}
          lang={direction === "rtl" ? "he" : undefined}
        >
          {block.text}
        </p>
      ) : null}

      {block.kind === "table" ? <TableBlock block={block} caption={caption} /> : null}

      {block.kind === "formula" ? (
        <pre className="content-formula" dir={direction}>
          <code>{block.text}</code>
        </pre>
      ) : null}

      {block.kind === "figure" ? (
        <FigureBlock block={block} direction={direction} />
      ) : null}

      <ProvenanceDetails block={block} />
    </article>
  );
}
