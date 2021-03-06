/* jshint node: true */

let gulp = require("gulp"),
    sequence = require("run-sequence"),
    config = require("./config.js"),
    paths = config.paths;

gulp.task("watch", cb => {
    sequence("build", "dowatch", cb);
});

gulp.task("dowatch", function(){
    // skip js transpile, making js builds much faster
    config.fastBuild = true;

    // transpile js
    gulp.watch(paths.src + "/**/*.js", ["babel"]);

    // copy html templates
    gulp.watch(paths.src + "/**/*.html", ["copyStatic"]);

    // copy static content
    gulp.watch(config.staticFiles, ["copyStatic"]);

    // copy translations
    gulp.watch(paths.staticSrc + "/i18n/*", ["copyStatic"]);

    // TODO - preprocess CSS
});

