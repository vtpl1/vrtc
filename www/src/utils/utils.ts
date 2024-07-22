// const getFormData = (object: { [x: string]: string; }) => Object.keys(object).reduce((formData, key) => {
//   formData.append(key, object[key]);
//   return formData;
// }, new FormData());
// type username = string;
// type password = string;
// type captcha = string;
// type getFormDataValues = username | password | captcha;
function getFormData(object: { [x: string]: string }) {
  const formData = new FormData();
  Object.keys(object).forEach((key) => formData.append(key, object[key]));
  return formData;
}
export default getFormData;
