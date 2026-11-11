/**
 * Brand: wraps each standalone word "vclu" in page text in <span class="vclu">,
 * which colours its first letter (dark-theme.css). Runs once on DOMContentLoaded.
 * "vclu.pl", "/opt/vclu", "vclu-cloud", "vclu_session" and "vclu:disk" are left alone.
 */
(function () {
    'use strict';

    var WORD = /(^|[^A-Za-z0-9_.\/:-])vclu(?=$|[^A-Za-z0-9_.\/:-]|[.:](?:$|[^A-Za-z0-9_]))/g;
    var SKIP = { SCRIPT: 1, STYLE: 1, CODE: 1, PRE: 1, TEXTAREA: 1, INPUT: 1, SELECT: 1, OPTION: 1, TITLE: 1, SVG: 1 };

    function skipped(node) {
        for (var el = node.parentNode; el && el.nodeType === 1; el = el.parentNode) {
            if (SKIP[el.nodeName.toUpperCase()] || el.hasAttribute('data-no-brand') ||
                (el.className === 'vclu')) {
                return true;
            }
        }
        return false;
    }

    function wrap(node) {
        var text = node.nodeValue, frag = document.createDocumentFragment(), last = 0, m;
        WORD.lastIndex = 0;
        while ((m = WORD.exec(text)) !== null) {
            var start = m.index + m[1].length;
            frag.appendChild(document.createTextNode(text.slice(last, start)));
            var span = document.createElement('span');
            span.className = 'vclu';
            span.textContent = 'vclu';
            frag.appendChild(span);
            last = start + 4;
        }
        if (last === 0) return;
        frag.appendChild(document.createTextNode(text.slice(last)));
        node.parentNode.replaceChild(frag, node);
    }

    document.addEventListener('DOMContentLoaded', function () {
        var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, null, false);
        var nodes = [], n;
        while ((n = walker.nextNode())) {
            if (n.nodeValue.indexOf('vclu') !== -1 && !skipped(n)) nodes.push(n);
        }
        for (var i = 0; i < nodes.length; i++) wrap(nodes[i]);
    });
})();
