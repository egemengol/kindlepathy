// Use browser.* API if available (Firefox), otherwise fall back to chrome.* (Chrome)
const browserAPI = typeof browser !== "undefined" ? browser : chrome;

// Server URL (single source of truth)
const SERVER_URL = "https://kindlepathy.com";

// DOM Elements
const loginSection = document.getElementById("loginSection");
const authenticatedSection = document.getElementById("authenticatedSection");
const loginButton = document.getElementById("loginButton");
const submitButton = document.getElementById("submitButton");
const libraryButton = document.getElementById("libraryButton");
const errorMessage = document.getElementById("errorMessage");

// Function to check if the user is authenticated
async function checkAuth() {
  try {
    const response = await fetch(`${SERVER_URL}/ext/check-auth`, {
      method: "GET",
      credentials: "include", // Include cookies in the request
    });

    if (!response.ok) {
      if (response.status === 401) {
        return { authenticated: false, error: null };
      } else {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
    }

    return { authenticated: true, error: null };
  } catch (err) {
    console.error("Error checking authentication:", err);
    if (
      err.message.includes("Failed to fetch") ||
      err.message.includes("NetworkError")
    ) {
      return { authenticated: false, error: "Server is down or unreachable." };
    } else {
      return {
        authenticated: false,
        error: err.message || "An error occurred.",
      };
    }
  }
}

// Function to send the article object to the server
async function sendArticleToServer(article) {
  const response = await fetch(`${SERVER_URL}/ext/article`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    credentials: "include",
    body: JSON.stringify(article),
  });

  if (response.status === 303) {
    const location = response.headers.get("Location");
    console.error("Redirect detected:", location);
    console.log(response.headers);
    throw new Error(`Redirect to ${location}`);
  }

  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }
}

// Function to extract content
async function extractContent() {
  const [activeTab] = await browserAPI.tabs.query({
    active: true,
    currentWindow: true,
  });
  if (!activeTab) {
    throw new Error("No active tab found.");
  }
  if (
    !activeTab.url ||
    activeTab.url.startsWith("chrome://") ||
    activeTab.url.startsWith("about:")
  ) {
    throw new Error("Unsupported tab URL.");
  }

  // Inject Readability.js
  await browserAPI.scripting.executeScript({
    target: { tabId: activeTab.id },
    files: ["Readability.js"],
  });

  // Inject DOMPurify.js
  await browserAPI.scripting.executeScript({
    target: { tabId: activeTab.id },
    files: ["DOMPurify.js"],
  });

  // Execute the extraction script
  const [result] = await browserAPI.scripting.executeScript({
    target: { tabId: activeTab.id },
    func: () => {
      try {
        // Clone the document to avoid modifying the original DOM
        const documentClone = document.cloneNode(true);

        // Log the original title for debugging
        const originalTitle = documentClone.title;
        // console.log("Original Title:", originalTitle);

        // Sanitize the cloned document using DOMPurify
        const sanitizedHTML = DOMPurify.sanitize(
          documentClone.documentElement.outerHTML,
        );

        // Create a new document from the sanitized HTML
        const parser = new DOMParser();
        const sanitizedDocument = parser.parseFromString(
          sanitizedHTML,
          "text/html",
        );

        // Log the sanitized title for debugging
        const sanitizedTitle = sanitizedDocument.title;
        // console.log("Sanitized Title:", sanitizedTitle);

        // Use Readability on the sanitized document
        const article = new Readability(sanitizedDocument).parse();

        // Ensure the title is preserved
        if (!article.title && originalTitle) {
          article.title = originalTitle;
        }

        return article;
      } catch (err) {
        throw new Error("Error during extraction:", err);
      }
    },
  });

  // Handle the result
  if (result && result.result) {
    return { article: result.result, url: activeTab.url };
  } else {
    throw new Error("Failed to extract content: No result returned.");
  }
}

// Initialize the popup
async function init() {
  console.log("Initializing popup...");

  const authResult = await checkAuth();
  console.log("Auth result:", authResult);

  if (authResult.authenticated) {
    loginSection.style.display = "none";
    authenticatedSection.style.display = "block";

    try {
      const article = await extractContent();
      console.log("Article extracted:", article);
      submitButton.textContent = "Submit";
      submitButton.classList.remove("disabled");
      submitButton.addEventListener("click", async () => {
        try {
          await sendArticleToServer(article);
          errorMessage.style.display = "none";
          alert("Article submitted successfully!");
        } catch (err) {
          errorMessage.textContent = "Failed to submit article.";
          errorMessage.style.display = "block";
          console.error("Submit error:", err);
        }
      });
    } catch (err) {
      errorMessage.textContent = "Failed to extract article.";
      errorMessage.style.display = "block";
      console.error("Extraction error:", err);
    }
  } else if (authResult.error) {
    loginSection.style.display = "none";
    authenticatedSection.style.display = "none";
    errorMessage.textContent = authResult.error;
    errorMessage.style.display = "block";
  } else {
    loginSection.style.display = "block";
    authenticatedSection.style.display = "none";
    loginButton.addEventListener("click", () => {
      window.open(`${SERVER_URL}/login`, "_blank");
    });
  }

  libraryButton.addEventListener("click", () => {
    window.open(`${SERVER_URL}/library`, "_blank");
  });
}

// Run the initialization
init();
