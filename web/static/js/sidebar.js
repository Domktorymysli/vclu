/**
 * vCLU Sidebar Controller
 * Handles sidebar toggle and mobile drawer
 */

(function() {
    'use strict';

    // Elements
    const sidebar = document.getElementById('sidebar');
    const sidebarToggle = document.getElementById('sidebarToggle');
    const hamburgerBtn = document.getElementById('hamburgerBtn');
    const sidebarOverlay = document.getElementById('sidebarOverlay');

    // Storage keys
    const STORAGE_KEY_SIDEBAR = 'vclu-sidebar-expanded';

    // Initialize sidebar state from localStorage
    function initSidebar() {
        const isExpanded = localStorage.getItem(STORAGE_KEY_SIDEBAR) === 'true';
        if (isExpanded) {
            sidebar.classList.add('expanded');
        }
    }

    // Toggle sidebar expanded/collapsed
    function toggleSidebar() {
        sidebar.classList.toggle('expanded');
        const isExpanded = sidebar.classList.contains('expanded');
        localStorage.setItem(STORAGE_KEY_SIDEBAR, isExpanded);
    }

    // Toggle mobile sidebar
    function toggleMobileSidebar() {
        sidebar.classList.toggle('mobile-open');
        sidebarOverlay.classList.toggle('visible');
        document.body.style.overflow = sidebar.classList.contains('mobile-open') ? 'hidden' : '';
    }

    // Close mobile sidebar
    function closeMobileSidebar() {
        sidebar.classList.remove('mobile-open');
        sidebarOverlay.classList.remove('visible');
        document.body.style.overflow = '';
    }

    // Event listeners
    if (sidebarToggle) {
        sidebarToggle.addEventListener('click', toggleSidebar);
    }

    if (hamburgerBtn) {
        hamburgerBtn.addEventListener('click', toggleMobileSidebar);
    }

    if (sidebarOverlay) {
        sidebarOverlay.addEventListener('click', closeMobileSidebar);
    }

    // Handle window resize
    let resizeTimer;
    window.addEventListener('resize', function() {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(function() {
            if (window.innerWidth > 768) {
                closeMobileSidebar();
            }
        }, 100);
    });

    // Handle escape key to close mobile sidebar
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && sidebar.classList.contains('mobile-open')) {
            closeMobileSidebar();
        }
    });

    // Nav groups (collapsible sections)
    function initNavGroups() {
        document.querySelectorAll('.nav-group').forEach(function(group) {
            var header = group.querySelector('.nav-group-header');
            if (!header) return;

            // Restore state from localStorage (but keep open if child is active)
            var key = 'vclu-navgroup-' + group.id;
            var hasActive = group.querySelector('.nav-item.active');
            if (hasActive) {
                group.classList.add('open');
            } else if (localStorage.getItem(key) === 'open') {
                group.classList.add('open');
            }

            header.addEventListener('click', function() {
                group.classList.toggle('open');
                localStorage.setItem(key, group.classList.contains('open') ? 'open' : 'closed');
            });
        });
    }

    // Initialize on DOM ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function() { initSidebar(); initNavGroups(); });
    } else {
        initSidebar();
        initNavGroups();
    }

    // Export for external use
    window.vcluSidebar = {
        toggle: toggleSidebar,
        toggleMobile: toggleMobileSidebar,
        closeMobile: closeMobileSidebar
    };

})();
