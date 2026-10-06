/**
 * Writes text and optional HTML content to the clipboard
 * @param textContent The plain text content to write
 * @param htmlContent Optional HTML content to write
 * @returns Promise that resolves when the content is successfully written
 */
export async function writeToClipboard(
  textContent: string,
  htmlContent?: string,
): Promise<void> {
  try {
    if (htmlContent && navigator.clipboard && "write" in navigator.clipboard) {
      const blob = new Blob([htmlContent], { type: "text/html" });
      const clipboardItem = new ClipboardItem({ "text/html": blob });
      await navigator.clipboard.write([clipboardItem]);
      return;
    }
  } catch {
    // Fall back to plain text when the browser refuses formatted content.
  }
  await navigator.clipboard.writeText(textContent);
}
