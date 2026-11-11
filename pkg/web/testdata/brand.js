// Runs VCLU.brand from utils.js (argv[2]) on each line of stdin and prints one JSON array.
// A minimal DOM: esc() needs only a div's textContent and innerHTML.
global.document = {
    createElement: function() {
        var text = '';
        return {
            set textContent(v) { text = v; },
            get innerHTML() {
                return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
            }
        };
    }
};
global.window = global;
require(require('path').resolve(process.argv[2]));
var input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
console.log(JSON.stringify(input.map(function(s) { return window.VCLU.brand(s); })));
