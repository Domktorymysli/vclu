(function() {
    var token = document.querySelector('meta[name="csrf-token"]');
    if (!token) return;
    token = token.content;
    if (!token) return;
    var _fetch = window.fetch;
    window.fetch = function(url, opts) {
        opts = opts || {};
        if (opts.method && opts.method !== 'GET') {
            if (!opts.headers) opts.headers = {};
            if (opts.headers instanceof Headers) {
                opts.headers.set('X-CSRF-Token', token);
            } else {
                opts.headers['X-CSRF-Token'] = token;
            }
        }
        return _fetch(url, opts);
    };
})();
