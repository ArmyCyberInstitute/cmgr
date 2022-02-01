const puppeteer = require('puppeteer');

(async () => {
  const browser = await puppeteer.launch({args: ['--no-sandbox']});
  const page = await browser.newPage();

  //*
  await page.setRequestInterception(true);
  page.on('request', interceptedRequest => {
    console.log('[Chrome] Visiting ' + interceptedRequest.url());
    interceptedRequest.continue();
  });
  page.on('console', msg => {
    for (let i = 0; i < msg.args().length; ++i)
      console.log(`[Chrome] ${i}: ${msg.args()[i]}`);
  });
  page.on('pageerror', function(err) {
    console.log('[Chrome] Page error: ' + err);
  });
  page.on('error', function(err) {
    console.log('[Chrome] Error: ' + err);
  });
  //*/
  let url = process.argv[2];
  console.log('[Chrome] ' + url);
  await page.goto(url);
  await page.waitFor(5000);
  console.log('[Chrome] Shutting down');
  /*
  let c = await page.cookies()
  console.log("Cookies",c);
  */
  await browser.close();
})();
