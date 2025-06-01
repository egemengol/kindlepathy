import { Readability } from "@mozilla/readability";
import { JSDOM } from "jsdom";
import { Logger, type ILogObj } from "tslog";
import type { Serve, Server } from "bun";

const logger: Logger<ILogObj> = new Logger({
  name: "ReadabilityService",
  minLevel: process.env.READABILITY_LOG_LEVEL
    ? parseInt(process.env.READABILITY_LOG_LEVEL, 10)
    : 3,
  type: "pretty",
  prettyLogTemplate: "{{name}}\t{{dateIsoStr}}\t{{logLevelName}}\t",
});

async function handleFetch(req: Request): Promise<Response> {
  // ... (handleFetch implementation remains the same) ...
  if (req.method !== "POST") {
    return new Response(JSON.stringify({ error: "Method Not Allowed" }), {
      status: 405,
      headers: { "Content-Type": "application/json; charset=utf-8" },
    });
  }

  try {
    const htmlContent = await req.text();
    if (!htmlContent) {
      // NOTE: Changed to return 400 as discussed for the empty HTML test case
      //       If you prefer the old behavior (200 OK + null), revert this part.
      return new Response(
        JSON.stringify({ error: "Request body cannot be empty" }),
        {
          status: 400, // Changed from 200
          headers: { "Content-Type": "application/json; charset=utf-8" },
        },
      );
    }

    const documentUrl = req.headers.get("x-document-url") || undefined;
    const start = performance.now();
    const dom = new JSDOM(htmlContent, { url: documentUrl });
    const afterDom = performance.now();
    logger.debug(`Article dom in ${(afterDom - start).toFixed(2)}ms`);
    const reader = new Readability(dom.window.document);
    const article = reader.parse();
    logger.debug(
      `Article parsing completed in ${(performance.now() - afterDom).toFixed(2)}ms`,
    );

    if (!article) {
      // Readability couldn't parse anything meaningful
      return new Response(JSON.stringify(null), {
        status: 200, // Still 200 OK if parsing yields null
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }

    logger.info(`Article parsed successfully: ${article.title}`); // Added "successfully"
    return new Response(JSON.stringify(article), {
      status: 200,
      headers: { "Content-Type": "application/json; charset=utf-8" },
    });
  } catch (error: unknown) {
    // Log the actual error for better debugging
    logger.error(`Failed to process article`, error);
    // Return a generic error response to the client
    const errorPayload = { error: "Processing Failed" };
    // Optionally include details in non-production environments
    // if (process.env.NODE_ENV !== 'production' && error instanceof Error) {
    //   errorPayload.details = error.message;
    // }
    return new Response(JSON.stringify(errorPayload), {
      status: 500,
      headers: { "Content-Type": "application/json; charset=utf-8" },
    });
  }
}

function handleError(error: Error): Response {
  logger.error(`Server error encountered:`, error); // Log the full error
  return new Response(JSON.stringify({ error: "Internal Server Error" }), {
    status: 500,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}

// --- Server Options ---
let serverOptions = {
  fetch: handleFetch,
  error: handleError,
  port: undefined as number | undefined,
  hostname: undefined as string | undefined,
  unix: undefined as string | undefined,
};

// --- Argument Parsing (remains the same) ---
const udsArgIndex = process.argv.indexOf("--uds");
if (udsArgIndex !== -1 && process.argv.length > udsArgIndex + 1) {
  serverOptions.unix = process.argv[udsArgIndex + 1];
  logger.info(`Configured to listen on UDS: ${serverOptions.unix}`);
} else {
  serverOptions.port = parseInt(process.env.PORT || "3000", 10);
  serverOptions.hostname = process.env.HOSTNAME || "0.0.0.0";
  logger.info(
    `Configured to listen on ${serverOptions.hostname}:${serverOptions.port}`,
  );
}

// --- Start Server (remains the same) ---
const server: Server = Bun.serve(serverOptions as Serve<typeof serverOptions>);
logger.info(`Server process started successfully`);

// --- Shutdown Handler ---
// This is the critical part for test cleanup
const shutdown = async (signal: string) => {
  // Keep async
  logger.info(`Received signal: ${signal}. Shutting down server...`);
  let exitCode = 0; // Default to success

  try {
    // Attempt to stop the server gracefully first
    // Use a timeout for stopping the server gracefully, e.g., 500ms
    const stopPromise = server.stop(true); // true = close connections immediately
    const timeoutPromise = new Promise(
      (_, reject) =>
        setTimeout(() => reject(new Error("server.stop() timed out")), 500), // 500ms timeout
    );

    try {
      logger.debug("Waiting for server.stop()...");
      await Promise.race([stopPromise, timeoutPromise]);
      logger.info("Bun server stopped successfully.");
    } catch (stopError: any) {
      // This could be the timeout error or an error from server.stop() itself
      logger.error(
        "Error or timeout during server.stop():",
        stopError?.message || stopError,
      );
      // Don't necessarily exit with error code just because stop timed out,
      // process.exit() below is the main goal for cleanup.
      // We might set exitCode = 1 here if a *real* error occurred during stop.
      if (
        !(
          stopError instanceof Error &&
          stopError.message === "server.stop() timed out"
        )
      ) {
        exitCode = 1; // Set error code only if it wasn't the timeout
      }
    }
  } catch (err) {
    // Catch potential synchronous errors before or during server.stop setup
    logger.error("Unexpected error during shutdown initiation:", err);
    exitCode = 1;
  } finally {
    // *** CRUCIAL: Ensure process exits ***
    // This should happen regardless of whether server.stop succeeded, failed, or timed out.
    // This signals to the Go parent process that the child has terminated.
    logger.info(`Exiting process with code ${exitCode}.`);
    process.exit(exitCode); // Exit with appropriate code.
  }
};

// --- Signal Listeners ---
// No changes needed here
process.on("SIGINT", () => shutdown("SIGINT"));
process.on("SIGTERM", () => shutdown("SIGTERM"));

// Optional: Handle unhandled exceptions/rejections to prevent silent hangs
// (These remain the same)
process.on("uncaughtException", (error) => {
  logger.fatal("Uncaught Exception:", error);
  process.exit(1); // Exit with error code
});

process.on("unhandledRejection", (reason, promise) => {
  logger.fatal("Unhandled Rejection at:", promise, "reason:", reason);
  process.exit(1); // Exit with error code
});
