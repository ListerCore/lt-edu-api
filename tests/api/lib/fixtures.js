// Finds real data of the demo through the API itself, so the suites do not hard-code ids
const { api, query } = require('./client');

// teacherSheet: the first course of the teacher in the active period, with an open and a closed term
async function teacherSheet(slug = 'demo-ebr') {
  const teacher = api(slug, 'teacher');
  const book = await teacher.get('/gradebook');
  if (book.status !== 200 || !book.body.sheets?.length) throw new Error(`El docente de ${slug} no tiene cursos en la demo`);
  const sheet = book.body.sheets[0];
  const terms = book.body.period.terms;
  const openTerm = terms.find((t) => t.status === 'open');
  const closedTerm = terms.find((t) => t.status === 'closed');
  const detail = await teacher.get(`/assignments/${sheet.assignment_id}/sheet${query({ term_id: openTerm.id })}`);
  return {
    periodId: book.body.period.id,
    assignmentId: sheet.assignment_id,
    sectionId: sheet.section_id,
    openTermId: openTerm?.id,
    closedTermId: closedTerm?.id,
    studentId: detail.body.sheet.students[0].id,
    competencyId: detail.body.sheet.competencies[0].id,
  };
}

// firstStudent: one student id of the institution, read with a role that can list them
async function firstStudent(slug, role) {
  const res = await api(slug, role).get(`/students${query({ limit: 1 })}`);
  if (res.status !== 200 || !res.body.students?.length) throw new Error(`No hay estudiantes en ${slug}`);
  return res.body.students[0];
}

module.exports = { teacherSheet, firstStudent };
