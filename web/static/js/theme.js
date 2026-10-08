/**
 * vCLU Theme Toggle
 * Uses Bootstrap 5.3 built-in dark mode (data-bs-theme attribute)
 */

const STORAGE_KEY_THEME = 'vclu-theme';

// Get saved theme or detect system preference
function getPreferredTheme() {
    const saved = localStorage.getItem(STORAGE_KEY_THEME);
    if (saved) {
        return saved;
    }
    // Default to dark theme
    return 'dark';
}

// Apply theme to document
function setTheme(theme) {
    document.documentElement.setAttribute('data-bs-theme', theme);
    localStorage.setItem(STORAGE_KEY_THEME, theme);

    // Update toggle button icon visibility (handled by CSS)
    const toggleBtn = document.getElementById('themeToggle');
    if (toggleBtn) {
        toggleBtn.setAttribute('title', theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode');
    }
}

// Toggle between light and dark
function toggleTheme() {
    const current = document.documentElement.getAttribute('data-bs-theme');
    const newTheme = current === 'dark' ? 'light' : 'dark';
    setTheme(newTheme);
}

// Initialize theme on page load
document.addEventListener('DOMContentLoaded', function() {
    setTheme(getPreferredTheme());

    // Bind toggle button
    const toggleBtn = document.getElementById('themeToggle');
    if (toggleBtn) {
        toggleBtn.addEventListener('click', toggleTheme);
    }
});

// Apply theme immediately (before DOMContentLoaded) to prevent flash
(function() {
    setTheme(getPreferredTheme());
})();
